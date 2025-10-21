#!/bin/bash
# The script downgrades the go version in go.mod to the major.minor.0 format (e.g., 1.24.7 becomes 1.24.0).
# It commits the changes to the repository with the message "UPSTREAM: <drop>: Downgrade go patch version".

set -e
set -o pipefail

stage_and_commit(){
    local commit_message="$1"

    # If committer email and name is passed as environment variable then use it.
    if [[ -z "$REBASEBOT_GIT_USERNAME" || -z "$REBASEBOT_GIT_EMAIL" ]]; then
        author_flag=()
    else
        author_flag=(--author="$REBASEBOT_GIT_USERNAME <$REBASEBOT_GIT_EMAIL>")
    fi

    if [[ -n $(git status --porcelain) ]]; then
        git add -A
        git commit "${author_flag[@]}" -q -m "$commit_message"
    fi
}

main(){
    if [[ ! -f "go.mod" ]]; then
        echo "go.mod file not found" >&2
        exit 1
    fi

    # Extract the current go version from go.mod
    local current_version=$(grep "^go " go.mod | awk '{print $2}')

    if [[ -z "$current_version" ]]; then
        echo "no go version found in go.mod" >&2
        exit 1
    fi

    # Extract major and minor version (e.g., from 1.24.7, extract 1.24)
    local major_minor=$(echo "$current_version" | cut -d. -f1-2)

    # Downgrade to major.minor.0 format
    local downgraded_version="${major_minor}.0"

    # Replace the go version in go.mod
    sed -i "s/^go .*/go ${downgraded_version}/" go.mod

    stage_and_commit "UPSTREAM: <drop>: Downgrade go version"
}

main
