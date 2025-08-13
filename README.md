# scip-lsp

A language agnostic language server that uses [SCIP]{github.com/sourcegraph/scip} as it's index source. Built with Bazel for Bazel.

## Overview

This project includes a Go based Language Server that handles ingesting SCIP files, as well as a SCIP generator for Bazel.

## Prerequisites

- Bazel
  - bin/bazel will ensure you have a copy of Bazelisk installed for use in the repo
  - Bazelisk will use the correct version based on .bazelversion
- Direnv
  - Helps keep path and environment up to date when working in this repo
- JDK 11 or newer
- Bash shell

## Manual Setup Steps

Below is an overview of the key components needed to get working language intelligence for Java files in this repo.

We will focus on a) automating these steps b) adjusting them to ensure they allow for easy setup in any repo.

1. Package and install the VS Code/Cursor Extension
   - `npm install -g vsce`
   - `cd ./src/extension && vsce package` -> yes to any prompts
   - Right click on the resulting artifact from the VS Code/Cursor file explorer, and click "Install Extension VSIX"
   - Add `"uber.uberLanguageServer.serverInfoFile": "/your/home/directory/.ulspd"` to VS Code/Cursor Machine settings.  Use the full path, as ~ doesn't resolve here.
     - To open settings, Cmd+Shift+P -> `Open VS Code Settings` (Cursor) or `Preferences: Open Settings` (VS Code). Select Machine (if present) or User tab at the top.
2. SCIP Index Generation
   - Create `.ijwb/.bazelproject` at the repo root
   - Sample contents:
     ```
     targets:
       //...
     ```
   - Add `.ijwb/.bazelproject` to .gitignore
   - Run `bazel run //bsp_server/scip_sync_util:scip_sync -- --cwd=/path/to/repo/root`
   - Confirm that `.scip` directory has now been added at the repo root
3. Language Server Launch
   - `bazel run //src/ulsp:ulsp-daemon`, in a separate terminal so it stays running
   - Cmd+Shift+P -> `SCIP LSP: Restart SCIP Language Server`
   - At this point, code intelligence should be available for Java files that are within the .bazelproject scope


## Manual Setup Steps

Below is an overview of the key components needed to get working language intelligence for Java files in this repo.

We will focus on a) automating these steps b) adjusting them to ensure they allow for easy setup in any repo.

1. Package and install the VS Code/Cursor Extension
   - `npm install -g vsce`
   - `~/scip-lsp/src/extension && vsce package` -> yes to any prompts
   - Right click on the resulting artifact from the VS Code/Cursor file explorer, and click "Install Extension VSIX"
   - Add `"uber.uberLanguageServer.serverInfoFile": "/your/home/directory/.ulspd"` to VS Code/Cursor Machine settings.  Use the full path, as ~ doesn't resolve here.
     - To open settings, Cmd+Shift+P -> `Open VS Code Settings` (Cursor) or `Preferences: Open Settings` (VS Code). Select Machine (if present) or User tab at the top.

2. Language Server Launch
   - `bazel run //src/ulsp:ulsp-daemon`, in a separate terminal so it stays running
   - Cmd+Shift+P -> `SCIP LSP: Restart SCIP Language Server`
   - At this point, code intelligence should be available for Java files that are within the .bazelproject scope

3. [If Indexing Code in Another Repo] Add necessary dependencies to the repo's MODULE.bazel
    ```
    bazel_dep(name = "rules_jvm_external", version = "6.7")
    local_repository = use_repo_rule("@bazel_tools//tools/build_defs/repo:local.bzl", "local_repository")
    local_repository(name = "scip_lsp", path = "/home/user/scip-lsp")

    maven = use_extension("@rules_jvm_external//:extensions.bzl", "maven")
    maven.install(
      artifacts = [
          # SCIP Java dependencies
          "com.sourcegraph:scip-java_2.13:0.10.4",
          "com.sourcegraph:scip-semanticdb:0.10.4",
          "com.sourcegraph:scip-java-proto:0.10.4",
          "com.sourcegraph:semanticdb-java:0.10.4",
          "com.sourcegraph:semanticdb-javac:0.10.4",

          # Intellij dependencies
          "com.jetbrains.intellij.java:java-decompiler-engine:jar:251.26094.121",

          # Utility libraries
          "commons-cli:commons-cli:1.5.0",
          "commons-io:commons-io:2.11.0",
          "org.jspecify:jspecify:0.3.0",
          "org.ow2.asm:asm:9.7.1",
          "org.projectlombok:lombok:1.18.38",

          # Logging
          "org.slf4j:slf4j-api:2.0.9",
          "org.slf4j:slf4j-simple:2.0.9",
      ],
      repositories = [
          "https://repo1.maven.org/maven2/",
          "https://www.jetbrains.com/intellij-repository/releases",
      ],
    )
    use_repo(maven, "maven")
    ```

4. SCIP Index Generation
   - Create `.ijwb/.bazelproject` at the repo root, add it to .gitignore.
     - Sample contents:
       ```
       targets:
         //...
       ```
   - From within the repo to be indexed, run:
     `/path/to/scip-lsp/tools/generate_scip_index.sh --cwd=/path/to/your/repo/root`
   - Confirm that `.scip` directory has now been added at the repo root

## Current limitations
- You must clone this project repo alongside the repo you want to navigate, on the same machine
- The other repo must also be bzlmod-enabled (WORKSPACE support coming soon)

## Building the Project

To build the project:

```bash
bazel build //...
```

To run the tests:

```bash
bazel test //...
```

## Project Structure

- `src/main/java/com/uber/scip/aggregator/` - Core SCIP aggregator implementation
    - `Aggregator.java` - Main entry point for SCIP aggregation
    - `FileAnalyzer.java` - Analyzes Java files for SCIP indexing
    - `scip/` - SCIP-specific implementations for indexing
- `scripts/` - Utility scripts
- `BUILD.bazel` - Root Bazel build file
- `MODULE.bazel` - Bazel module definition (Bzlmod)

## Dependencies

This project uses the following key dependencies:

- Sourcegraph SCIP Java version 0.10.4
- JUnit 5 and Mockito for testing
- SLF4J for logging
- Commons CLI for command-line processing

## Adding More Code

1. Create a new Java file in the appropriate package under `src/main/java/`
2. Update the corresponding `BUILD.bazel` file to include your new source file
3. Run `bazel build //...` to verify it builds correctly

## SCIP Java Integration

The project integrates with [scip-java](https://github.com/sourcegraph/scip-java) version 0.10.4 to process semanticdb files and generate SCIP indices. This enables advanced code intelligence features when used with compatible tools like Sourcegraph.

## Contributors

Thanks to the following contributors who got this started as an internal project at Uber, before transitioning this to open source:
- demianenkoa (SCIP indexing flow)
- shuklahy (Core code intelligence LSP workflows)
- jamydev (Fast SCIP consumption & tooling, SCIP<->LSP Bridge)
- mnoah1 (Language server + VS Code extension overall integration/setup)
- prathshenoy (Language server maintenance/improvements)
- amishra-u (Repo prep for open source)
