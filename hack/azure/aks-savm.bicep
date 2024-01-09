// AKS (Azure CNI & StandaloneVirtualMachines) & ACR

param aksname string = 'karpenter' // also used for vnet
param acrname string = 'karpenter'
param location string = resourceGroup().location
param dnsPrefix string = aksname

resource vnet 'Microsoft.Network/virtualNetworks@2022-05-01' = {
  location: location
  name: aksname
  properties: {
    addressSpace: { addressPrefixes: ['10.0.0.0/8'] }
    subnets: [
      {
        name: 'nodesubnet'
        properties: { addressPrefix: '10.240.0.0/16' }
      }
/*      
      {
        name: 'podsubnet'
        properties: {
            addressPrefix: '10.241.0.0/16'
            delegations: [
              {
                name: 'aks-delegation'
                properties: {
                  serviceName: 'Microsoft.ContainerService/managedClusters'
                }
              }
            ]
        }
      }
*/      
    ]
  }

  resource nodesubnet 'subnets' existing = { name: 'nodesubnet' }
//resource podsubnet  'subnets' existing = { name: 'podsubnet' }
}

resource aks 'Microsoft.ContainerService/managedClusters@2023-01-02-preview' = {
  location: location
  name: aksname
  identity: {
    type: 'SystemAssigned'
  }
  sku: {
    name: 'Basic'
    tier: 'Paid'  // better for scale perf testing
  }
  properties: {
    dnsPrefix: dnsPrefix
    agentPoolProfiles: [
      {
        count: 1
        mode: 'System'
        name: 'nodepool1'
        type: 'VirtualMachines' // experimental
        vmSize: 'Standard_D2pds_v5'
        maxPods: 30
        vnetSubnetID: vnet::nodesubnet.id
//      podSubnetID: vnet::podsubnet.id
      }
    ]
    networkProfile: {
      networkPlugin: 'azure'
      serviceCidr: '10.0.0.0/16'
      dnsServiceIP: '10.0.0.10'
      dockerBridgeCidr: '172.17.0.1/16'
    } 
    "oidcIssuerProfile": {
      "enabled": true
    }
    "workloadIdentity": {
      "enabled": true
    }
  }
}

// container registry

resource acr 'Microsoft.ContainerRegistry/registries@2021-09-01' = {
  location: location
  name: acrname
  sku: { name: 'Basic' }
}

var AcrPull = subscriptionResourceId('Microsoft.Authorization/roleDefinition', '7f951dda-4ed3-4680-a7ca-43fe172d538d')

@description('AKS can pull images from ACR')
resource aksAcrPull 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, acr.name, aks.name, AcrPull)
  scope: acr
  properties: {
    principalId: aks.properties.identityProfile.kubeletidentity.objectId
    principalType: 'ServicePrincipal'
    roleDefinitionId: AcrPull
  }
}
