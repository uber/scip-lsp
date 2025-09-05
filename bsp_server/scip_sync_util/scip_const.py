# -*- coding: utf-8 -*-

ALL_TARGETS = "//..."

# Aspect related paths
ASPECT_SCIP_INDEX = "@scip_lsp//bsp_server/indexer:scip.bzl%scip_java_aspect"
ASPECT_OUTPUT_GROUPS = "--output_groups=scip"

# Index constants
INDEX_FILE_SUFFIX = ".index_mutated.scip"
INDEX_GENERATION_MNEMONIC = "scipMutator"

SCIP_CLI_UEXEC_PATH = "tools/uexec/scip/scip-cli"

SCIP_TOOLING_TARGET = "@scip_lsp//src/main/java/com/uber/scip/aggregator:aggregator_bin"

JDK_SCIP_FILE_PREFIX = "jdk_temurin"
SHA256_FILE_SUFFIX = ".sha256"
WORKSPACE_FILE_NAME = "workspace.json"

JAVA_VERSION_FLAGS = [
    "--java_language_version=17",
    "--java_runtime_version=remotejdk_17",
    "--tool_java_language_version=17",
    "--tool_java_runtime_version=remotejdk_17",
]


BAZEL = "bazel"
QUERY = "query"
BUILD = "build"

# target info
SCIP_TARGET_SUFFIX = "_scip_index"

# Note we mention native rules instead of uber macros
# this allows us to execute kind query on bazel
SUPPORTED_RULES = ["java_library", "java_import", "java_test", "jvm_import"]

# bazelproject section names
TARGETS = "targets"
DIRECTORIES = "directories"
DERIVE_TARGETS_FROM_DIRECTORIES = "derive_targets_from_directories"
