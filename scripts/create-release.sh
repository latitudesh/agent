#!/bin/bash

# Create Release Script for Latitude.sh Agent
# Usage: ./scripts/create-release.sh v1.0.0

set -e

VERSION=$1

if [ -z "$VERSION" ]; then
    echo "Usage: $0 <version>"
    echo "Example: $0 v1.0.0"
    exit 1
fi

# Validate version format
if [[ ! $VERSION =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "Error: Version must be in format vX.Y.Z (e.g., v1.0.0)"
    exit 1
fi

echo "Creating release $VERSION..."

# Make sure we're on the right branch
CURRENT_BRANCH=$(git branch --show-current)
echo "Current branch: $CURRENT_BRANCH"

# Check if working directory is clean
if ! git diff-index --quiet HEAD --; then
    echo "Error: Working directory is not clean. Please commit or stash changes."
    exit 1
fi

# Update version in main.go
echo "Updating version in main.go..."
sed -i.bak "s/const Version = \".*\"/const Version = \"${VERSION#v}\"/" cmd/agent/main.go
rm cmd/agent/main.go.bak

# Commit version update
git add cmd/agent/main.go
git commit -m "chore: bump version to $VERSION"

# Create and push tag
echo "Creating tag $VERSION..."
git tag -a "$VERSION" -m "Release $VERSION"

echo "Pushing changes and tag..."
git push origin "$CURRENT_BRANCH"
git push origin "$VERSION"

echo ""
echo "✅ Release $VERSION created successfully!"
echo ""
echo "🚀 GitHub Actions will now:"
echo "   1. Build binaries for Linux (amd64, arm64)"
echo "   2. Create GitHub release with assets"
echo "   3. Publish release notes"
echo ""
echo "🔗 Check the progress at:"
echo "   https://github.com/latitudesh/agent/actions"
echo ""
echo "📦 Once complete, the release will be available at:"
echo "   https://github.com/latitudesh/agent/releases/tag/$VERSION"
echo ""
echo "🎉 Users can then install with:"
echo "   curl -s https://raw.githubusercontent.com/latitudesh/agent/main/install.sh | sudo bash -s -- -firewall fw_XXX -project proj_XXX"