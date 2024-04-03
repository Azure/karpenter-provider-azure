param acrName string
param principalId string
param msiName string
param roleDefinitionID string

resource acrResource 'Microsoft.ContainerRegistry/registries@2023-01-01-preview' existing = {
  name: acrName
}

var roleAssignmentName = guid(msiName, roleDefinitionID, resourceGroup().id)
resource roleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: roleAssignmentName
  scope: acrResource
  properties: {
    roleDefinitionId: resourceId('Microsoft.Authorization/roleDefinitions', roleDefinitionID)
    principalId: principalId
  }
}
