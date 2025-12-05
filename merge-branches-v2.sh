#!/bin/bash

echo "Git Branch Merger Script"
echo "========================"

# Get target branch name
echo -n "Enter target branch name: "
read TARGET_BRANCH
if [ -z "$TARGET_BRANCH" ]; then
    echo "Error: Target branch name cannot be empty"
    exit 1
fi

echo "Target branch: $TARGET_BRANCH"

# Get list of source branches
echo
echo "Enter source branch names (space-separated): "
read -a BRANCHES

if [ ${#BRANCHES[@]} -eq 0 ]; then
    echo "Error: No branches provided"
    exit 1
fi

echo "Processing ${#BRANCHES[@]} branches: ${BRANCHES[*]}"
echo

# Clean up any leftover temporary branches from previous runs
echo "Cleaning up any leftover temporary branches..."
for branch in "${BRANCHES[@]}"; do
    git branch -D "temp_squash_$branch" 2>/dev/null || true
done

# Function to restore branch states on error
cleanup_and_exit() {
    echo
    echo "Script stopped due to error. You may need to manually clean up."
    echo "To restore original branch states manually, run these commands:"
    for branch in "${BRANCHES[@]}"; do
        echo "  git checkout $branch && git reset --hard origin/$branch"
    done
    exit 1
}

# Set trap for cleanup on error (but disable it for conflict handling)
trap cleanup_and_exit ERR

# Fetch latest from origin and switch to main
echo "Fetching latest from origin..."
git fetch origin || { echo "Failed to fetch from origin"; exit 1; }

echo "Switching to main and pulling latest changes..."
git checkout main || { echo "Failed to checkout main"; exit 1; }
git pull || { echo "Failed to pull main"; exit 1; }

# Create or checkout target branch from main
echo "Creating/checking out target branch: $TARGET_BRANCH"
if git show-ref --verify --quiet "refs/heads/$TARGET_BRANCH"; then
    echo "Target branch exists, checking out and resetting to main"
    git checkout "$TARGET_BRANCH"
    git reset --hard main
else
    echo "Creating new target branch"
    git checkout -b "$TARGET_BRANCH" main
fi

# Process each branch
for CURRENT_BRANCH in "${BRANCHES[@]}"; do
    echo
    echo "Processing branch: $CURRENT_BRANCH"
    
    # Check if branch exists locally or on origin
    if ! git show-ref --verify --quiet "refs/heads/$CURRENT_BRANCH"; then
        # Branch doesn't exist locally, check if it exists on origin
        if git show-ref --verify --quiet "refs/remotes/origin/$CURRENT_BRANCH"; then
            echo "Branch $CURRENT_BRANCH not found locally, creating from origin..."
            git checkout -b "$CURRENT_BRANCH" "origin/$CURRENT_BRANCH"
        else
            echo "Error: Branch $CURRENT_BRANCH does not exist locally or on origin"
            exit 1
        fi
    fi
    
    # Checkout the source branch
    git checkout "$CURRENT_BRANCH" || { echo "Failed to checkout $CURRENT_BRANCH"; exit 1; }
    
    # Pull latest changes
    git pull || { echo "Failed to pull $CURRENT_BRANCH"; exit 1; }
    
    # Store the original HEAD for later restoration
    ORIGINAL_HEAD=$(git rev-parse HEAD)
    
    # Since branches have merged main, we need to get just the feature commits
    # We'll create a diff patch against main and apply it as one commit
    echo "Creating diff against main..."
    
    # Check if branch is different from main
    if git diff --quiet main.."$CURRENT_BRANCH"; then
        echo "Branch $CURRENT_BRANCH has no differences from main, skipping..."
        continue
    fi
    
    # Create a temporary commit with all the differences from main
    echo "Creating squashed representation of changes..."
    
    # Get the diff and apply it to a temporary branch based on main
    git checkout main
    
    # Clean up any existing temporary branch
    git branch -D "temp_squash_$CURRENT_BRANCH" 2>/dev/null || true
    
    git checkout -b "temp_squash_$CURRENT_BRANCH"
    
    # Apply all the changes from the feature branch
    # Replace forward slashes in branch name for safe file naming
    SAFE_BRANCH_NAME=$(echo "$CURRENT_BRANCH" | sed 's|/|_|g')
    PATCH_FILE="/tmp/branch_changes_$SAFE_BRANCH_NAME.patch"
    
    git diff main.."$CURRENT_BRANCH" > "$PATCH_FILE"
    
    # Apply the patch
    if ! git apply "$PATCH_FILE"; then
        echo "Failed to apply patch for $CURRENT_BRANCH"
        git checkout "$CURRENT_BRANCH"
        git branch -D "temp_squash_$CURRENT_BRANCH" 2>/dev/null || true
        rm -f "$PATCH_FILE"
        exit 1
    fi
    
    # Stage all changes and commit
    git add -A
    git commit -m "Squashed commits from $CURRENT_BRANCH" || { 
        echo "Failed to create squash commit"; 
        git checkout "$CURRENT_BRANCH"
        git branch -D "temp_squash_$CURRENT_BRANCH" 2>/dev/null || true
        rm -f "$PATCH_FILE"
        exit 1; 
    }
    
    # Get the squash commit hash
    SQUASH_COMMIT=$(git rev-parse HEAD)
    
    # Switch to target branch and cherry-pick the squash commit
    git checkout "$TARGET_BRANCH"
    
    echo "Cherry-picking squashed commit..."
    # Disable error trap for cherry-pick to handle conflicts manually
    set +e
    git cherry-pick "$SQUASH_COMMIT"
    CHERRY_PICK_EXIT=$?
    set -e
    
    # Clean up temporary branch and patch file
    git branch -D "temp_squash_$CURRENT_BRANCH" 2>/dev/null || true
    rm -f "$PATCH_FILE"
    
    if [ $CHERRY_PICK_EXIT -ne 0 ]; then
        echo
        echo "CONFLICT DETECTED while processing $CURRENT_BRANCH!"
        echo "Current state:"
        echo "  - You are on branch: $TARGET_BRANCH"
        echo "  - Cherry-pick is in progress"
        echo
        echo "Please resolve conflicts and then run:"
        echo "  git cherry-pick --continue"
        echo
        echo "Or abort this cherry-pick:"
        echo "  git cherry-pick --abort"
        echo
        echo "After resolving, you can manually restore remaining branches:"
        for remaining_branch in "${BRANCHES[@]}"; do
            echo "  git checkout $remaining_branch && git reset --hard origin/$remaining_branch"
        done
        exit 1
    fi
    
    # Restore the original branch state
    echo "Restoring original branch state..."
    git checkout "$CURRENT_BRANCH"
    git reset --hard "$ORIGINAL_HEAD"
    
    echo "Successfully processed $CURRENT_BRANCH"
done

echo
echo "SUCCESS: All branches have been processed!"
echo "Target branch \"$TARGET_BRANCH\" now contains all changes."
echo "Original branches have been restored to their original state."
echo
echo "Done! No changes have been pushed to remote."