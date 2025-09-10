# ulsp-daemon

ulsp is a continuously running local process that can provide language server support to all running ide instances on a given machine.

## Running/Debugging Locally
### To launch latest version at checked out HEAD
The Extension will attempt to launch its own instance of the ulsp service from the version deployed on Artifactory if it detects that one is not already running.  To test changes with the latest version, launch it using the following steps:
1. Add this settings `"uber.uberLanguageServer.serverDevelopmentMode": true` into your VS Code settings.json to prevent relaunch of ulsp daemon by vscode-devportal extension.
2. Check for any existing running ulsp process (`ps -aux | grep ulsp`) and terminate if necessary.
3. Launch the service by running one of the following from the root of the service directory.
   - `$ ULSP_ENVIRONMENT=development ULSP_CONFIG_DIR=$PWD/config bazel run .`
   - `$ ULSP_ENVIRONMENT=development ULSP_CONFIG_DIR=$PWD/config bazel debug .`
4. Open a separate IDE session with the Uber Dev Portal extension installed.  The IDE will connect to the running instance and begin sending requests to ulsp, which will appear in the terminal output from ulsp when running it in development mode.  The service will pause at breakpoints if launched in debug mode.
5. If you make changes to ulsp and restart it, you can trigger the IDE to re-connect to the updated service by running `Cmd+Shift+P -> Developer: Reload Window` which will cause the IDE to re-establish the LSP connection.


### To launch current deployed version
Same steps as above, except use the following command to launch via uexec:
   - `$ ULSP_ENVIRONMENT=development ULSP_CONFIG_DIR=~/go-code/src/ulsp/config uexec ~/go-code/tools/ide/ulsp/ulsp-daemon`

ULSP_CONFIG_DIR can be pointed to another path if different configs are to be used, and may include multiple colon-separated paths if configs may be in multiple locations.

### Service Configs
Configurations are selected based on the provided ULSP_CONFIG_DIR location, and ULSP_ENVIRONMENT value.
- base.yaml is always used as the base
- if ULSP_ENVIRONMENT is set to `development`, the development.yaml config will applied on top of the base config.
- otherwise, devpod.yaml or local.yaml will be applied on top of the base config based on current environment.
- see [decorators.go](src/ulsp/app/decorators.go) for logic.

## Deploying Updates

TODO(JamyDev): Open Source Releasing Process
