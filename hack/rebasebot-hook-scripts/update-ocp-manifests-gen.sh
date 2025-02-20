#!/bin/bash
# This script is run by rebasebot during the rebase of the CAPI repositories.
# This script must be added to rebasebot as a post-rebase hook, which will download it from this repository and execute it on the repository being rebased.
# The repository being rebased must have a Make target under the openshift folder that performs the OCP manifests-gen tool update.

set -e  # Exit immediately if a command exits with a non-zero status
set -o pipefail  # Return the exit status of the last command in the pipe that failed

stage_and_commit(){
    local commit_message="$1"

    # If commiter email and name is passed as environment variable then use it.
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
    pushd openshift
    make update-manifests-gen
    popd
    stage_and_commit "UPSTREAM: <drop>: Update manifests generator"
}

main
