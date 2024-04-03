Deployed using Cloud Shell in Azure with the following command:

`az deployment group create  --resource-group 1es-resources --template-file 1es-karpenter-deploy.bicep --subscription 70b66d46-132a-446b-ae4c-6502fe9bea3f`

Note: had to rerun this as it failed on the role assignment initially since it takes a bit of time for the msi to propagate.