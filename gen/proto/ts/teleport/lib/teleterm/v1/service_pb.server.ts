/* eslint-disable */
// @generated by protobuf-ts 2.9.3 with parameter long_type_number,eslint_disable,add_pb_suffix,ts_nocheck,server_generic
// @generated from protobuf file "teleport/lib/teleterm/v1/service.proto" (package "teleport.lib.teleterm.v1", syntax proto3)
// tslint:disable
// @ts-nocheck
//
//
// Teleport
// Copyright (C) 2023  Gravitational, Inc.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>.
//
import { AuthenticateWebDeviceResponse } from "./service_pb";
import { AuthenticateWebDeviceRequest } from "./service_pb";
import { UpdateUserPreferencesResponse } from "./service_pb";
import { UpdateUserPreferencesRequest } from "./service_pb";
import { GetUserPreferencesResponse } from "./service_pb";
import { GetUserPreferencesRequest } from "./service_pb";
import { ListUnifiedResourcesResponse } from "./service_pb";
import { ListUnifiedResourcesRequest } from "./service_pb";
import { GetConnectMyComputerNodeNameResponse } from "./service_pb";
import { GetConnectMyComputerNodeNameRequest } from "./service_pb";
import { DeleteConnectMyComputerNodeResponse } from "./service_pb";
import { DeleteConnectMyComputerNodeRequest } from "./service_pb";
import { WaitForConnectMyComputerNodeJoinResponse } from "./service_pb";
import { WaitForConnectMyComputerNodeJoinRequest } from "./service_pb";
import { CreateConnectMyComputerNodeTokenResponse } from "./service_pb";
import { CreateConnectMyComputerNodeTokenRequest } from "./service_pb";
import { CreateConnectMyComputerRoleResponse } from "./service_pb";
import { CreateConnectMyComputerRoleRequest } from "./service_pb";
import { UpdateHeadlessAuthenticationStateResponse } from "./service_pb";
import { UpdateHeadlessAuthenticationStateRequest } from "./service_pb";
import { ReportUsageEventRequest } from "./usage_events_pb";
import { FileTransferProgress } from "./service_pb";
import { FileTransferRequest } from "./service_pb";
import { LogoutRequest } from "./service_pb";
import { RpcInputStream } from "@protobuf-ts/runtime-rpc";
import { RpcOutputStream } from "@protobuf-ts/runtime-rpc";
import { LoginPasswordlessResponse } from "./service_pb";
import { LoginPasswordlessRequest } from "./service_pb";
import { LoginRequest } from "./service_pb";
import { GetClusterRequest } from "./service_pb";
import { AuthSettings } from "./auth_settings_pb";
import { GetAuthSettingsRequest } from "./service_pb";
import { SetGatewayLocalPortRequest } from "./service_pb";
import { SetGatewayTargetSubresourceNameRequest } from "./service_pb";
import { RemoveGatewayRequest } from "./service_pb";
import { Gateway } from "./gateway_pb";
import { CreateGatewayRequest } from "./service_pb";
import { ListGatewaysResponse } from "./service_pb";
import { ListGatewaysRequest } from "./service_pb";
import { RemoveClusterRequest } from "./service_pb";
import { Cluster } from "./cluster_pb";
import { AddClusterRequest } from "./service_pb";
import { GetAppsResponse } from "./service_pb";
import { GetAppsRequest } from "./service_pb";
import { GetKubesResponse } from "./service_pb";
import { GetKubesRequest } from "./service_pb";
import { GetSuggestedAccessListsResponse } from "./service_pb";
import { GetSuggestedAccessListsRequest } from "./service_pb";
import { PromoteAccessRequestResponse } from "./service_pb";
import { PromoteAccessRequestRequest } from "./service_pb";
import { AssumeRoleRequest } from "./service_pb";
import { GetRequestableRolesResponse } from "./service_pb";
import { GetRequestableRolesRequest } from "./service_pb";
import { ReviewAccessRequestResponse } from "./service_pb";
import { ReviewAccessRequestRequest } from "./service_pb";
import { CreateAccessRequestResponse } from "./service_pb";
import { CreateAccessRequestRequest } from "./service_pb";
import { EmptyResponse } from "./service_pb";
import { DeleteAccessRequestRequest } from "./service_pb";
import { GetAccessRequestResponse } from "./service_pb";
import { GetAccessRequestRequest } from "./service_pb";
import { GetAccessRequestsResponse } from "./service_pb";
import { GetAccessRequestsRequest } from "./service_pb";
import { GetServersResponse } from "./service_pb";
import { GetServersRequest } from "./service_pb";
import { ListDatabaseUsersResponse } from "./service_pb";
import { ListDatabaseUsersRequest } from "./service_pb";
import { GetDatabasesResponse } from "./service_pb";
import { GetDatabasesRequest } from "./service_pb";
import { ListLeafClustersRequest } from "./service_pb";
import { ListClustersResponse } from "./service_pb";
import { ListClustersRequest } from "./service_pb";
import { UpdateTshdEventsServerAddressResponse } from "./service_pb";
import { UpdateTshdEventsServerAddressRequest } from "./service_pb";
import { ServerCallContext } from "@protobuf-ts/runtime-rpc";
/**
 * TerminalService is used by the Electron app to communicate with the tsh daemon.
 *
 * While we aim to preserve backwards compatibility in order to satisfy CI checks and follow the
 * proto practices used within the company, this service is not guaranteed to be stable across
 * versions. The packaging process of Teleport Connect ensures that the server and the client use
 * the same version of the service.
 *
 * @generated from protobuf service teleport.lib.teleterm.v1.TerminalService
 */
export interface ITerminalService<T = ServerCallContext> {
    /**
     * UpdateTshdEventsServerAddress lets the Electron app update the address the tsh daemon is
     * supposed to use when connecting to the tshd events gRPC service. This RPC needs to be made
     * before any other from this service.
     *
     * The service is supposed to return a response from this call only after the client is ready.
     *
     * @generated from protobuf rpc: UpdateTshdEventsServerAddress(teleport.lib.teleterm.v1.UpdateTshdEventsServerAddressRequest) returns (teleport.lib.teleterm.v1.UpdateTshdEventsServerAddressResponse);
     */
    updateTshdEventsServerAddress(request: UpdateTshdEventsServerAddressRequest, context: T): Promise<UpdateTshdEventsServerAddressResponse>;
    /**
     * ListRootClusters lists root clusters
     * Does not include detailed cluster information that would require a network request.
     *
     * @generated from protobuf rpc: ListRootClusters(teleport.lib.teleterm.v1.ListClustersRequest) returns (teleport.lib.teleterm.v1.ListClustersResponse);
     */
    listRootClusters(request: ListClustersRequest, context: T): Promise<ListClustersResponse>;
    /**
     * ListLeafClusters lists leaf clusters
     * Does not include detailed cluster information that would require a network request.
     *
     * @generated from protobuf rpc: ListLeafClusters(teleport.lib.teleterm.v1.ListLeafClustersRequest) returns (teleport.lib.teleterm.v1.ListClustersResponse);
     */
    listLeafClusters(request: ListLeafClustersRequest, context: T): Promise<ListClustersResponse>;
    /**
     * GetDatabases returns a filtered and paginated list of databases
     *
     * @generated from protobuf rpc: GetDatabases(teleport.lib.teleterm.v1.GetDatabasesRequest) returns (teleport.lib.teleterm.v1.GetDatabasesResponse);
     */
    getDatabases(request: GetDatabasesRequest, context: T): Promise<GetDatabasesResponse>;
    /**
     * ListDatabaseUsers lists allowed users for the given database based on the role set.
     *
     * @generated from protobuf rpc: ListDatabaseUsers(teleport.lib.teleterm.v1.ListDatabaseUsersRequest) returns (teleport.lib.teleterm.v1.ListDatabaseUsersResponse);
     */
    listDatabaseUsers(request: ListDatabaseUsersRequest, context: T): Promise<ListDatabaseUsersResponse>;
    /**
     * GetServers returns filtered, sorted, and paginated servers
     *
     * @generated from protobuf rpc: GetServers(teleport.lib.teleterm.v1.GetServersRequest) returns (teleport.lib.teleterm.v1.GetServersResponse);
     */
    getServers(request: GetServersRequest, context: T): Promise<GetServersResponse>;
    /**
     * GetAccessRequests lists filtered AccessRequests
     *
     * @generated from protobuf rpc: GetAccessRequests(teleport.lib.teleterm.v1.GetAccessRequestsRequest) returns (teleport.lib.teleterm.v1.GetAccessRequestsResponse);
     */
    getAccessRequests(request: GetAccessRequestsRequest, context: T): Promise<GetAccessRequestsResponse>;
    /**
     * GetAccessRequest retreives a single Access Request
     *
     * @generated from protobuf rpc: GetAccessRequest(teleport.lib.teleterm.v1.GetAccessRequestRequest) returns (teleport.lib.teleterm.v1.GetAccessRequestResponse);
     */
    getAccessRequest(request: GetAccessRequestRequest, context: T): Promise<GetAccessRequestResponse>;
    /**
     * DeleteAccessRequest deletes the access request by id
     *
     * @generated from protobuf rpc: DeleteAccessRequest(teleport.lib.teleterm.v1.DeleteAccessRequestRequest) returns (teleport.lib.teleterm.v1.EmptyResponse);
     */
    deleteAccessRequest(request: DeleteAccessRequestRequest, context: T): Promise<EmptyResponse>;
    /**
     * CreateAccessRequest creates an access request
     *
     * @generated from protobuf rpc: CreateAccessRequest(teleport.lib.teleterm.v1.CreateAccessRequestRequest) returns (teleport.lib.teleterm.v1.CreateAccessRequestResponse);
     */
    createAccessRequest(request: CreateAccessRequestRequest, context: T): Promise<CreateAccessRequestResponse>;
    /**
     * ReviewAccessRequest submits a review for an Access Request
     *
     * @generated from protobuf rpc: ReviewAccessRequest(teleport.lib.teleterm.v1.ReviewAccessRequestRequest) returns (teleport.lib.teleterm.v1.ReviewAccessRequestResponse);
     */
    reviewAccessRequest(request: ReviewAccessRequestRequest, context: T): Promise<ReviewAccessRequestResponse>;
    /**
     * GetRequestableRoles gets all requestable roles
     *
     * @generated from protobuf rpc: GetRequestableRoles(teleport.lib.teleterm.v1.GetRequestableRolesRequest) returns (teleport.lib.teleterm.v1.GetRequestableRolesResponse);
     */
    getRequestableRoles(request: GetRequestableRolesRequest, context: T): Promise<GetRequestableRolesResponse>;
    /**
     * AssumeRole assumes the role of the given access request
     *
     * @generated from protobuf rpc: AssumeRole(teleport.lib.teleterm.v1.AssumeRoleRequest) returns (teleport.lib.teleterm.v1.EmptyResponse);
     */
    assumeRole(request: AssumeRoleRequest, context: T): Promise<EmptyResponse>;
    /**
     * PromoteAccessRequest promotes an access request to an access list.
     *
     * @generated from protobuf rpc: PromoteAccessRequest(teleport.lib.teleterm.v1.PromoteAccessRequestRequest) returns (teleport.lib.teleterm.v1.PromoteAccessRequestResponse);
     */
    promoteAccessRequest(request: PromoteAccessRequestRequest, context: T): Promise<PromoteAccessRequestResponse>;
    /**
     * GetSuggestedAccessLists returns suggested access lists for an access request.
     *
     * @generated from protobuf rpc: GetSuggestedAccessLists(teleport.lib.teleterm.v1.GetSuggestedAccessListsRequest) returns (teleport.lib.teleterm.v1.GetSuggestedAccessListsResponse);
     */
    getSuggestedAccessLists(request: GetSuggestedAccessListsRequest, context: T): Promise<GetSuggestedAccessListsResponse>;
    /**
     * GetKubes returns filtered, sorted, and paginated kubes
     *
     * @generated from protobuf rpc: GetKubes(teleport.lib.teleterm.v1.GetKubesRequest) returns (teleport.lib.teleterm.v1.GetKubesResponse);
     */
    getKubes(request: GetKubesRequest, context: T): Promise<GetKubesResponse>;
    /**
     * GetApps returns a filtered and paginated list of apps.
     *
     * @generated from protobuf rpc: GetApps(teleport.lib.teleterm.v1.GetAppsRequest) returns (teleport.lib.teleterm.v1.GetAppsResponse);
     */
    getApps(request: GetAppsRequest, context: T): Promise<GetAppsResponse>;
    /**
     * AddCluster adds a cluster to profile
     *
     * @generated from protobuf rpc: AddCluster(teleport.lib.teleterm.v1.AddClusterRequest) returns (teleport.lib.teleterm.v1.Cluster);
     */
    addCluster(request: AddClusterRequest, context: T): Promise<Cluster>;
    /**
     * RemoveCluster removes a cluster from profile
     *
     * @generated from protobuf rpc: RemoveCluster(teleport.lib.teleterm.v1.RemoveClusterRequest) returns (teleport.lib.teleterm.v1.EmptyResponse);
     */
    removeCluster(request: RemoveClusterRequest, context: T): Promise<EmptyResponse>;
    /**
     * ListGateways lists gateways
     *
     * @generated from protobuf rpc: ListGateways(teleport.lib.teleterm.v1.ListGatewaysRequest) returns (teleport.lib.teleterm.v1.ListGatewaysResponse);
     */
    listGateways(request: ListGatewaysRequest, context: T): Promise<ListGatewaysResponse>;
    /**
     * CreateGateway creates a gateway
     *
     * @generated from protobuf rpc: CreateGateway(teleport.lib.teleterm.v1.CreateGatewayRequest) returns (teleport.lib.teleterm.v1.Gateway);
     */
    createGateway(request: CreateGatewayRequest, context: T): Promise<Gateway>;
    /**
     * RemoveGateway removes a gateway
     *
     * @generated from protobuf rpc: RemoveGateway(teleport.lib.teleterm.v1.RemoveGatewayRequest) returns (teleport.lib.teleterm.v1.EmptyResponse);
     */
    removeGateway(request: RemoveGatewayRequest, context: T): Promise<EmptyResponse>;
    /**
     * SetGatewayTargetSubresourceName changes the TargetSubresourceName field of gateway.Gateway
     * and returns the updated version of gateway.Gateway.
     *
     * In Connect this is used to update the db name of a db connection along with the CLI command.
     *
     * @generated from protobuf rpc: SetGatewayTargetSubresourceName(teleport.lib.teleterm.v1.SetGatewayTargetSubresourceNameRequest) returns (teleport.lib.teleterm.v1.Gateway);
     */
    setGatewayTargetSubresourceName(request: SetGatewayTargetSubresourceNameRequest, context: T): Promise<Gateway>;
    /**
     * SetGatewayLocalPort starts a new gateway on the new port, stops the old gateway and then
     * assigns the URI of the old gateway to the new one. It does so without fetching a new db cert.
     *
     * @generated from protobuf rpc: SetGatewayLocalPort(teleport.lib.teleterm.v1.SetGatewayLocalPortRequest) returns (teleport.lib.teleterm.v1.Gateway);
     */
    setGatewayLocalPort(request: SetGatewayLocalPortRequest, context: T): Promise<Gateway>;
    /**
     * GetAuthSettings returns cluster auth settigns
     *
     * @generated from protobuf rpc: GetAuthSettings(teleport.lib.teleterm.v1.GetAuthSettingsRequest) returns (teleport.lib.teleterm.v1.AuthSettings);
     */
    getAuthSettings(request: GetAuthSettingsRequest, context: T): Promise<AuthSettings>;
    /**
     * GetCluster returns cluster. Makes a network request and includes detailed
     * information about enterprise features availabed on the connected auth server
     *
     * @generated from protobuf rpc: GetCluster(teleport.lib.teleterm.v1.GetClusterRequest) returns (teleport.lib.teleterm.v1.Cluster);
     */
    getCluster(request: GetClusterRequest, context: T): Promise<Cluster>;
    /**
     * Login logs in a user to a cluster
     *
     * @generated from protobuf rpc: Login(teleport.lib.teleterm.v1.LoginRequest) returns (teleport.lib.teleterm.v1.EmptyResponse);
     */
    login(request: LoginRequest, context: T): Promise<EmptyResponse>;
    /**
     * LoginPasswordless logs in a user to a cluster passwordlessly.
     *
     * The RPC is streaming both ways and the message sequence example for hardware keys are:
     * (-> means client-to-server, <- means server-to-client)
     *
     * Hardware keys:
     * -> Init
     * <- Send PasswordlessPrompt enum TAP to choose a device
     * -> Receive TAP device response
     * <- Send PasswordlessPrompt enum PIN
     * -> Receive PIN response
     * <- Send PasswordlessPrompt enum RETAP to confirm
     * -> Receive RETAP device response
     * <- Send list of credentials (e.g. usernames) associated with device
     * -> Receive the index number associated with the selected credential in list
     * <- End
     *
     * @generated from protobuf rpc: LoginPasswordless(stream teleport.lib.teleterm.v1.LoginPasswordlessRequest) returns (stream teleport.lib.teleterm.v1.LoginPasswordlessResponse);
     */
    loginPasswordless(requests: RpcOutputStream<LoginPasswordlessRequest>, responses: RpcInputStream<LoginPasswordlessResponse>, context: T): Promise<void>;
    /**
     * ClusterLogin logs out a user from cluster
     *
     * @generated from protobuf rpc: Logout(teleport.lib.teleterm.v1.LogoutRequest) returns (teleport.lib.teleterm.v1.EmptyResponse);
     */
    logout(request: LogoutRequest, context: T): Promise<EmptyResponse>;
    /**
     * TransferFile sends a request to download/upload a file
     *
     * @generated from protobuf rpc: TransferFile(teleport.lib.teleterm.v1.FileTransferRequest) returns (stream teleport.lib.teleterm.v1.FileTransferProgress);
     */
    transferFile(request: FileTransferRequest, responses: RpcInputStream<FileTransferProgress>, context: T): Promise<void>;
    /**
     * ReportUsageEvent allows to send usage events that are then anonymized and forwarded to prehog
     *
     * @generated from protobuf rpc: ReportUsageEvent(teleport.lib.teleterm.v1.ReportUsageEventRequest) returns (teleport.lib.teleterm.v1.EmptyResponse);
     */
    reportUsageEvent(request: ReportUsageEventRequest, context: T): Promise<EmptyResponse>;
    /**
     * UpdateHeadlessAuthenticationState updates a headless authentication resource's state.
     * An MFA challenge will be prompted when approving a headless authentication.
     *
     * @generated from protobuf rpc: UpdateHeadlessAuthenticationState(teleport.lib.teleterm.v1.UpdateHeadlessAuthenticationStateRequest) returns (teleport.lib.teleterm.v1.UpdateHeadlessAuthenticationStateResponse);
     */
    updateHeadlessAuthenticationState(request: UpdateHeadlessAuthenticationStateRequest, context: T): Promise<UpdateHeadlessAuthenticationStateResponse>;
    /**
     * CreateConnectMyComputerRole creates a role which allows access to nodes with the label
     * teleport.dev/connect-my-computer/owner: <cluster user> and allows logging in to those nodes as
     * the current system user.
     *
     * @generated from protobuf rpc: CreateConnectMyComputerRole(teleport.lib.teleterm.v1.CreateConnectMyComputerRoleRequest) returns (teleport.lib.teleterm.v1.CreateConnectMyComputerRoleResponse);
     */
    createConnectMyComputerRole(request: CreateConnectMyComputerRoleRequest, context: T): Promise<CreateConnectMyComputerRoleResponse>;
    /**
     * CreateConnectMyComputerNodeToken creates a node join token that is valid for 5 minutes
     *
     * @generated from protobuf rpc: CreateConnectMyComputerNodeToken(teleport.lib.teleterm.v1.CreateConnectMyComputerNodeTokenRequest) returns (teleport.lib.teleterm.v1.CreateConnectMyComputerNodeTokenResponse);
     */
    createConnectMyComputerNodeToken(request: CreateConnectMyComputerNodeTokenRequest, context: T): Promise<CreateConnectMyComputerNodeTokenResponse>;
    /**
     * WaitForConnectMyComputerNodeJoin sets up a watcher and returns a response only after detecting
     * that the Connect My Computer node for the particular cluster has joined the cluster (the
     * OpPut event).
     *
     * This RPC times out by itself after a minute to prevent the request from hanging forever, in
     * case the client didn't set a deadline or doesn't abort the request.
     *
     * @generated from protobuf rpc: WaitForConnectMyComputerNodeJoin(teleport.lib.teleterm.v1.WaitForConnectMyComputerNodeJoinRequest) returns (teleport.lib.teleterm.v1.WaitForConnectMyComputerNodeJoinResponse);
     */
    waitForConnectMyComputerNodeJoin(request: WaitForConnectMyComputerNodeJoinRequest, context: T): Promise<WaitForConnectMyComputerNodeJoinResponse>;
    /**
     * DeleteConnectMyComputerNode deletes the Connect My Computer node.
     *
     * @generated from protobuf rpc: DeleteConnectMyComputerNode(teleport.lib.teleterm.v1.DeleteConnectMyComputerNodeRequest) returns (teleport.lib.teleterm.v1.DeleteConnectMyComputerNodeResponse);
     */
    deleteConnectMyComputerNode(request: DeleteConnectMyComputerNodeRequest, context: T): Promise<DeleteConnectMyComputerNodeResponse>;
    /**
     * GetConnectMyComputerNodeName reads the Connect My Computer node name (UUID) from a disk.
     *
     * @generated from protobuf rpc: GetConnectMyComputerNodeName(teleport.lib.teleterm.v1.GetConnectMyComputerNodeNameRequest) returns (teleport.lib.teleterm.v1.GetConnectMyComputerNodeNameResponse);
     */
    getConnectMyComputerNodeName(request: GetConnectMyComputerNodeNameRequest, context: T): Promise<GetConnectMyComputerNodeNameResponse>;
    /**
     * ListUnifiedResources retrieves a paginated list of all resource types displayable in the UI.
     *
     * @generated from protobuf rpc: ListUnifiedResources(teleport.lib.teleterm.v1.ListUnifiedResourcesRequest) returns (teleport.lib.teleterm.v1.ListUnifiedResourcesResponse);
     */
    listUnifiedResources(request: ListUnifiedResourcesRequest, context: T): Promise<ListUnifiedResourcesResponse>;
    /**
     * GetUserPreferences returns the combined (root + leaf cluster) preferences for a given user.
     *
     * @generated from protobuf rpc: GetUserPreferences(teleport.lib.teleterm.v1.GetUserPreferencesRequest) returns (teleport.lib.teleterm.v1.GetUserPreferencesResponse);
     */
    getUserPreferences(request: GetUserPreferencesRequest, context: T): Promise<GetUserPreferencesResponse>;
    /**
     * UpdateUserPreferences updates the preferences for a given user in appropriate root and leaf clusters.
     * Only the properties that are set (cluster_preferences, unified_resource_preferences) will be updated.
     *
     * @generated from protobuf rpc: UpdateUserPreferences(teleport.lib.teleterm.v1.UpdateUserPreferencesRequest) returns (teleport.lib.teleterm.v1.UpdateUserPreferencesResponse);
     */
    updateUserPreferences(request: UpdateUserPreferencesRequest, context: T): Promise<UpdateUserPreferencesResponse>;
    /**
     * AuthenticateWebDevice blesses a web session with device trust by performing
     * the on-behalf-of device authentication ceremony.
     *
     * See
     * https://github.com/gravitational/teleport.e/blob/master/rfd/0009e-device-trust-web-support.md#device-web-authentication.
     *
     * @generated from protobuf rpc: AuthenticateWebDevice(teleport.lib.teleterm.v1.AuthenticateWebDeviceRequest) returns (teleport.lib.teleterm.v1.AuthenticateWebDeviceResponse);
     */
    authenticateWebDevice(request: AuthenticateWebDeviceRequest, context: T): Promise<AuthenticateWebDeviceResponse>;
}
