package azureoidc

import (
	"context"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/gravitational/trace"
	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
	"github.com/microsoftgraph/msgraph-sdk-go/applicationtemplates"
	"github.com/microsoftgraph/msgraph-sdk-go/models"
	"github.com/microsoftgraph/msgraph-sdk-go/serviceprincipals"
)

// A special application template ID in Microsoft Graph, equivalent to the "create your own application" option in Azure portal.
// Only non-gallery apps ("Create your own application" option in the UI) are allowed to use SAML SSO,
// hence we use this template.
// Ref: https://learn.microsoft.com/en-us/graph/api/applicationtemplate-instantiate
const nonGalleryAppTemplateID = "8adf8e6e-67b2-4cf2-a259-e3dc5476c621"

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

	displayName := "Teleport" + " " + strings.TrimPrefix(proxyPublicAddr, "https://")

	instantiateRequest := applicationtemplates.NewItemInstantiatePostRequestBody()
	instantiateRequest.SetDisplayName(&displayName)
	appAndSP, err := graphClient.ApplicationTemplates().
		ByApplicationTemplateId(nonGalleryAppTemplateID).
		Instantiate().
		Post(ctx, instantiateRequest, nil)

	if err != nil {
		return appID, tenantID, trace.Wrap(err, "failed to instantiate application template")
	}

	app := appAndSP.GetApplication()
	sp := appAndSP.GetServicePrincipal()
	appID = *app.GetAppId()
	spID := *sp.GetId()

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

	if err := createFederatedAuthCredential(ctx, graphClient, *app.GetId(), proxyPublicAddr); err != nil {
		return appID, tenantID, trace.Wrap(err, "failed to create an OIDC federated auth credential")
	}

	if err := setupSSO(ctx, graphClient, appID, *app.GetId(), spID, proxyPublicAddr); err != nil {
		return appID, tenantID, trace.Wrap(err, "failed to set up SSO for the enterprise app")
	}

	return appID, tenantID, nil
}

func setupSSO(ctx context.Context, graphClient *msgraphsdk.GraphServiceClient, appID string, appObjectID string, spID string, proxyPublicAddr string) error {
	// Set service principal to prefer SAML sign on
	spPatch := models.NewServicePrincipal()
	preferredSingleSignOnMode := "saml"
	spPatch.SetPreferredSingleSignOnMode(&preferredSingleSignOnMode)

	_, err := graphClient.ServicePrincipals().
		ByServicePrincipalId(spID).
		Patch(ctx, spPatch, nil)

	if err != nil {
		return trace.Wrap(err, "failed to enable SSO for service principal")
	}

	// Add SAML urls
	app := models.NewApplication()
	samlURI, err := url.Parse(proxyPublicAddr)
	if err != nil {
		return trace.Wrap(err)
	}
	samlURI.Path = "/v1/webapi/saml/acs/ad"
	app.SetIdentifierUris([]string{samlURI.String()})
	webApp := models.NewWebApplication()
	webApp.SetRedirectUris([]string{samlURI.String()})
	app.SetWeb(webApp)

	_, err = graphClient.Applications().
		ByApplicationId(appObjectID).
		Patch(ctx, app, nil)

	if err != nil {
		return trace.Wrap(err, "failed to set SAML URIs")
	}

	// Add a SAML signing certificate
	certRequest := serviceprincipals.NewItemAddTokenSigningCertificatePostRequestBody()
	// Display name is required to start with `CN=`.
	// Ref: https://learn.microsoft.com/en-us/graph/api/serviceprincipal-addtokensigningcertificate
	displayName := "CN=azure-sso"
	certRequest.SetDisplayName(&displayName)

	cert, err := graphClient.ServicePrincipals().
		ByServicePrincipalId(spID).
		AddTokenSigningCertificate().
		Post(ctx, certRequest, nil)

	if err != nil {
		trace.Wrap(err, "failed to set up a signing certificate")
	}

	// Set the preferred SAML signing key
	spPatch = models.NewServicePrincipal()
	spPatch.SetPreferredTokenSigningKeyThumbprint(cert.GetThumbprint())

	_, err = graphClient.ServicePrincipals().
		ByServicePrincipalId(spID).
		Patch(ctx, spPatch, nil)

	if err != nil {
		return trace.Wrap(err, "failed to enable SSO for service principal")
	}

	return nil
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
