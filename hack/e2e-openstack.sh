#!/usr/bin/env bash

# TODO(stephenfin): This is legacy from when the OpenStack e2e tests lived
# elsewhere. We should remove this script and update the CI jobs to call the
# Makefile target like everyone else

exec make e2e
