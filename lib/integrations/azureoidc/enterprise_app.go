package azureoidc

import (
	"context"

	"github.com/google/uuid"
	"github.com/gravitational/trace"
	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
	"github.com/microsoftgraph/msgraph-sdk-go/models"
	"github.com/microsoftgraph/msgraph-sdk-go/serviceprincipals"
)

// Ref: https://learn.microsoft.com/en-us/graph/permissions-reference
var appRoles = []string{
	// Application.Read.All
	"9a5d68dd-52b0-4cc2-bd40-abcf44ac3a30",
	// Directory.Read.All
	"7ab1d382-f21e-4acd-a863-ba3e13f7da61",
	// Policy.Read.All
	"246dd0d5-5bd0-4def-940b-0421030a5b68",
}

func SetupEnterpriseApp(ctx context.Context, proxyPublicAddr string) (string, string, error) {
	var appID, tenantID string

	tenantID, err := getTenantID()
	if err != nil {
		return appID, tenantID, trace.Wrap(err)
	}

	graphClient, err := createGraphClient()
	if err != nil {
		return appID, tenantID, trace.Wrap(err)
	}

	displayName := "Teleport" + " " + proxyPublicAddr

	app := models.NewApplication()
	app.SetDisplayName(&displayName)

	createdApp, err := graphClient.Applications().Post(ctx, app, nil)
	if err != nil {
		return appID, tenantID, trace.Wrap(err, "failed to create an application")
	}
	appID = *createdApp.GetAppId()

	sp := models.NewServicePrincipal()
	sp.SetAppId(&appID)

	createdSP, err := graphClient.ServicePrincipals().Post(ctx, sp, nil)
	if err != nil {
		return appID, tenantID, trace.Wrap(err, "failed to create a service principal")
	}
	spID := *createdSP.GetId()

	msGraphResourceID, err := getMSGraphResourceID(ctx, graphClient)
	if err != nil {
		return appID, tenantID, trace.Wrap(err, "failed to get MS Graph API resource ID")
	}

	msGraphResourceUUID := uuid.MustParse(msGraphResourceID)

	for _, appRoleID := range appRoles {
		assignment := models.NewAppRoleAssignment()

		spUUID := uuid.MustParse(spID)
		assignment.SetPrincipalId(&spUUID)

		assignment.SetResourceId(&msGraphResourceUUID)

		appRoleUUID := uuid.MustParse(appRoleID)
		assignment.SetAppRoleId(&appRoleUUID)
		_, err := graphClient.ServicePrincipals().
			ByServicePrincipalId(spID).
			AppRoleAssignments().
			Post(ctx, assignment, nil)
		if err != nil {
			return appID, tenantID, trace.Wrap(err, "failed to assign app role %s", appRoleID)
		}
	}

	if err := createFederatedAuthCredential(ctx, graphClient, *createdApp.GetId(), proxyPublicAddr); err != nil {
		return appID, tenantID, trace.Wrap(err, "failed to create an OIDC federated auth credential")
	}

	return appID, tenantID, nil
}

func createFederatedAuthCredential(ctx context.Context, graphClient *msgraphsdk.GraphServiceClient, appObjectID string, proxyPublicAddr string) error {
	credential := models.NewFederatedIdentityCredential()
	name := "teleport-oidc"
	audiences := []string{azureDefaultJWTAudience}
	subject := azureSubject
	credential.SetName(&name)
	credential.SetIssuer(&proxyPublicAddr)
	credential.SetAudiences(audiences)
	credential.SetSubject(&subject)

	// ByApplicationID here means the object ID,
	// i.e. app.GetId(), not app.GetAppId().
	_, err := graphClient.Applications().ByApplicationId(appObjectID).
		FederatedIdentityCredentials().Post(ctx, credential, nil)

	return trace.Wrap(err)

}

func getMSGraphResourceID(ctx context.Context, graphClient *msgraphsdk.GraphServiceClient) (string, error) {
	requestFilter := "displayName eq 'Microsoft Graph'"

	requestParameters := &serviceprincipals.ServicePrincipalsRequestBuilderGetQueryParameters{
		Filter: &requestFilter,
	}
	configuration := &serviceprincipals.ServicePrincipalsRequestBuilderGetRequestConfiguration{
		QueryParameters: requestParameters,
	}
	spResponse, err := graphClient.ServicePrincipals().Get(ctx, configuration)
	if err != nil {
		return "", trace.Wrap(err)
	}

	spList := spResponse.GetValue()
	if len(spList) < 1 {
		return "", trace.NotFound("Microsoft Graph app not found in the tenant")
	}

	return *spList[0].GetId(), nil
}
