#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"

"$script_dir/package_release_test.sh"
"$script_dir/install_cli_test.sh"
"$script_dir/deploy_server_test.sh"
"$script_dir/publish_release_test.sh"
