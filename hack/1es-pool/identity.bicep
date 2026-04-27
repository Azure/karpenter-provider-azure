// Subscription-scoped role assignment for the 1ES pool managed identity.
//
// This must be a separate module from deploy.bicep because role assignments
// at subscription scope require `targetScope = 'subscription'`, while the
// pool and identity resources are deployed at resource-group scope.
//
// Pattern borrowed from Azure/azure-service-operator (scripts/v2/cipool/identity.bicep).

targetScope = 'subscription'

@description('The principalId of the managed identity')
param principalId string

@description('The principal type of the identity')
param principalType string

@description('The roleDefinitionId of the role to grant')
param roleDefinitionId string

var roleAssignmentName = guid(subscription().id, principalId, roleDefinitionId)
resource roleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: roleAssignmentName
  properties: {
    roleDefinitionId: resourceId('Microsoft.Authorization/roleDefinitions', roleDefinitionId)
    principalType: principalType
    principalId: principalId
  }
}
