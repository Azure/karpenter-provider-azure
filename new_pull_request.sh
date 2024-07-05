# Create a new branch
git checkout -b feature/add-configure-acr-script

# Add the new script file
git add hack/deploy/configure-acr.sh

# Commit the changes
git commit -m "Add script for configuring a custom Azure Container Registry (ACR) for Karpenter managed nodes"

# Push the branch to the remote repository
git push origin feature/add-configure-acr-script

# Create a pull request
gh pr create --title "Add script for configuring a custom ACR for Karpenter managed nodes" --body "This PR adds a script to configure a custom Azure Container Registry (ACR) for Karpenter managed nodes."
