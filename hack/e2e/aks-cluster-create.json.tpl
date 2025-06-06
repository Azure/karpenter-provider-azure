{
  "location": "${ENV_AZURE_LOCATION}",
  "identity": {
    "type": "SystemAssigned"
  },
  "properties": {
    "dnsPrefix": "myaks",
    "agentPoolProfiles": [
      {
        "name": "systempool",
        "count": 3,
        "vmSize": "Standard_D8s_v5",
        "osType": "Linux",
        "mode": "System"
      }
    ],
    "nodeProvisioningProfile": {
      "mode": "Auto",
      "defaultNodePools": "None"
    },
    "networkProfile": {
      "networkPlugin": "azure",
      "networkPluginMode": "overlay",
      "networkDataplane": "cilium"
    },
  }
} 