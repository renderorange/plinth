#!/bin/bash

set -e

REPO="renderorange/plinth"
BRANCH="main"

echo "Setting up branch protection for $REPO ($BRANCH branch)..."

# Require pull request before merging
gh api repos/$REPO/branches/$BRANCH/protection \
  --method PUT \
  --input - <<'EOF'
{
  "required_status_checks": {
    "strict": true,
    "contexts": ["test", "build"]
  },
  "enforce_admins": true,
  "required_pull_request_reviews": {
    "required_approving_review_count": 1,
    "dismiss_stale_reviews": true
  },
  "restrictions": null,
  "allow_force_pushes": false,
  "allow_deletions": false
}
EOF

echo "Branch protection configured successfully!"
echo ""
echo "Settings applied:"
echo "- Require pull request before merging"
echo "- Require status checks to pass (test, build)"
echo "- Require branches to be up to date before merging"
echo "- Dismiss stale reviews when new commits are pushed"
echo "- Do not allow bypassing the above settings"
echo "- Do not allow force pushes"
echo "- Do not allow deletions"