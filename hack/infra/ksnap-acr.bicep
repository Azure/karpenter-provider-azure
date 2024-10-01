param location string = resourceGroup().location

@description('ACR for Karpenter snapshots')
resource acr 'Microsoft.ContainerRegistry/registries@2023-11-01-preview' = {
  name: 'ksnap'
  location: location
  sku: {
    name: 'Standard'
  }
  properties: {
    anonymousPullEnabled: true
    adminUserEnabled: false
  }
}

var schedule = '0 1 * * Tue' // 1am UTC every Tuesday

// --untagged appears to be broken: https://github.com/Azure/acr-cli/issues/131
var purgeOldArtifacts = '''
version: v1.1.0
steps:
- cmd: acr purge --filter 'karpenter/snapshot/.*:.*' --ago 30d
  disableWorkingDirectoryOverride: true
  timeout: 3600
'''

@description('purge old artifacts from the registry periodically')
resource acrPurgeTask 'Microsoft.ContainerRegistry/registries/tasks@2019-06-01-preview' = {
  name: 'purge-old-artifacts'
  location: location
  parent: acr
  properties: {
    platform: {
      os: 'Linux'
      architecture: 'amd64'
    }
    step: {
      type: 'EncodedTask'
      encodedTaskContent: base64(purgeOldArtifacts)
     }
     trigger: {
      timerTriggers: [{
          name: 'weekly-purge'
          schedule: schedule
          status: 'Enabled'
        }]
    }
    status: 'Enabled'
  }
}
