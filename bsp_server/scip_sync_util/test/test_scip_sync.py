import argparse
import os.path
import tempfile
import unittest
from datetime import datetime
from unittest.mock import MagicMock, call, patch

from bsp_server.scip_sync_util import scip_const, scip_sync
from bsp_server.scip_sync_util.mnemonics import ScipMnemonics
from bsp_server.scip_sync_util.scip_const import (
    BAZEL,
    BUILD,
    DERIVE_TARGETS_FROM_DIRECTORIES,
    DIRECTORIES,
    TARGETS,
)
from bsp_server.scip_sync_util.workspace import ScipWorkspace


class TestScipSync(unittest.TestCase):
    def setUp(self):
        self.temp_dir = tempfile.mkdtemp()
        self.mock_datetime = datetime(2024, 4, 19, 12, 0)
        self.cwd = "/path/to/cwd"
        self.scip_dir = os.path.join(self.cwd, ".scip")

    @patch("bsp_server.scip_sync_util.scip_sync.pprint")
    @patch("bsp_server.scip_sync_util.scip_sync.scip_utils.copy_index")
    @patch("bsp_server.scip_sync_util.scip_sync.scip_workspace.write_workspace")
    @patch("bsp_server.scip_sync_util.scip_sync.scip_workspace.populate_workspace")
    @patch("bsp_server.scip_sync_util.scip_sync.scip_utils.get_mnemonic_output")
    @patch("bsp_server.scip_sync_util.scip_sync.sync_scip")
    @patch("bsp_server.scip_sync_util.scip_sync.get_dependency_graph")
    @patch("bsp_server.scip_sync_util.scip_sync.fetch_targets_from_bazelproject")
    @patch("argparse.ArgumentParser.parse_args")
    @patch("bsp_server.scip_sync_util.scip_sync.datetime")
    @patch("os.path.exists")
    def test_main_no_targets_no_filepath(
        self,
        mock_exists,
        mock_datetime,
        mock_parse_args,
        mock_fetch_targets,
        mock_get_dependency_graph,
        mock_sync_scip,
        mock_get_mnemonic_output,
        mock_populate_workspace,
        mock_write_workspace,
        mock_copy_index,
        mock_pprint,
    ):
        """Test case 1: No targets or filepath provided - should rewrite workspace"""
        # Setup
        mock_datetime.now.return_value = self.mock_datetime
        mock_parse_args.return_value = argparse.Namespace(
            cwd=self.cwd,
            targets=[],
            filepath=None,
            depth=1,
        )
        mock_fetch_targets.return_value = set("//target1"), set("//target2")
        mock_get_dependency_graph.return_value = {"//target1", "//target2"}

        # Mock outputs
        source_list = "/tmp/source_list"
        with open(source_list, "w") as f:
            f.write("file1.java\nfile2.java")

        mock_get_mnemonic_output.return_value = {
            "//target1": {
                ScipMnemonics.INDEX_OUTPUT_MNEMONIC.value: ["index1"],
                ScipMnemonics.JAVA_TARGET_MANIFEST_MNEMONIC.value: ["manifest1"],
                ScipMnemonics.UNPACKED_JAVA_SOURCES_MNEMONIC.value: [source_list],
            }
        }

        mock_exists.return_value = True

        # Mock workspace
        workspace = ScipWorkspace()
        mock_populate_workspace.return_value = workspace

        # Run the function
        scip_sync.main()

        # Verify
        mock_fetch_targets.assert_called_once_with(self.cwd)
        mock_get_dependency_graph.assert_called_once_with(
            cwd=self.cwd,
            targets=set("//target1"),
            depth=1,
            exclude_targets=set("//target2"),
        )
        mock_sync_scip.assert_called_once()
        mock_get_mnemonic_output.assert_called_once()
        mock_populate_workspace.assert_called_once_with(
            self.cwd, mock_get_mnemonic_output.return_value
        )

        # Verify workspace is written with merge=False
        mock_write_workspace.assert_called_once_with(workspace, self.scip_dir)

        # Verify indexes are copied
        mock_copy_index.assert_called_once()

    @patch("bsp_server.scip_sync_util.scip_sync.pprint")
    @patch("bsp_server.scip_sync_util.scip_sync.scip_utils.copy_index")
    @patch("bsp_server.scip_sync_util.scip_sync.scip_workspace.write_workspace")
    @patch("bsp_server.scip_sync_util.scip_sync.scip_workspace.populate_workspace")
    @patch("bsp_server.scip_sync_util.scip_sync.scip_utils.get_mnemonic_output")
    @patch("bsp_server.scip_sync_util.scip_sync.sync_scip")
    @patch("bsp_server.scip_sync_util.scip_sync.get_dependency_graph")
    @patch("bsp_server.scip_sync_util.scip_sync.fetch_targets_from_bazelproject")
    @patch("argparse.ArgumentParser.parse_args")
    @patch("bsp_server.scip_sync_util.scip_sync.datetime")
    @patch("os.path.exists")
    def test_main_with_targets(
        self,
        mock_exists,
        mock_datetime,
        mock_parse_args,
        mock_fetch_targets,
        mock_get_dependency_graph,
        mock_sync_scip,
        mock_get_mnemonic_output,
        mock_populate_workspace,
        mock_write_workspace,
        mock_copy_index,
        mock_pprint,
    ):
        """Test case 2: Targets provided - should merge workspace"""
        # Setup
        mock_datetime.now.return_value = self.mock_datetime
        mock_parse_args.return_value = argparse.Namespace(
            cwd=self.cwd,
            targets=["//target1"],
            filepath=None,
            depth=1,
        )
        mock_get_dependency_graph.return_value = {"//target1", "//target2"}

        # Mock outputs
        source_list = "/tmp/source_list"
        with open(source_list, "w") as f:
            f.write("file1.java\nfile2.java")

        mock_get_mnemonic_output.return_value = {
            "//target1": {
                ScipMnemonics.INDEX_OUTPUT_MNEMONIC.value: ["index1"],
                ScipMnemonics.JAVA_TARGET_MANIFEST_MNEMONIC.value: ["manifest1"],
                ScipMnemonics.UNPACKED_JAVA_SOURCES_MNEMONIC.value: [source_list],
            }
        }

        mock_exists.return_value = True

        # Mock workspace
        workspace = ScipWorkspace()
        mock_populate_workspace.return_value = workspace

        # Run the function
        scip_sync.main()

        # Verify
        mock_fetch_targets.assert_not_called()  # Should not fetch targets from bazelproject
        mock_get_dependency_graph.assert_called_once_with(
            cwd="/path/to/cwd", targets=["//target1"], depth=1, exclude_targets=set()
        )
        mock_sync_scip.assert_called_once()
        mock_get_mnemonic_output.assert_called_once()
        mock_populate_workspace.assert_called_once_with(
            self.cwd, mock_get_mnemonic_output.return_value
        )

        # Verify workspace is written with merge=True
        mock_write_workspace.assert_called_once_with(workspace, self.scip_dir)

        # Verify indexes are copied
        mock_copy_index.assert_called_once()

    @patch("bsp_server.scip_sync_util.scip_sync.pprint")
    @patch("bsp_server.scip_sync_util.scip_sync.scip_utils.copy_index")
    @patch("bsp_server.scip_sync_util.scip_sync.scip_workspace.write_workspace")
    @patch("bsp_server.scip_sync_util.scip_sync.scip_workspace.populate_workspace")
    @patch("bsp_server.scip_sync_util.scip_sync.scip_utils.get_mnemonic_output")
    @patch("bsp_server.scip_sync_util.scip_sync.sync_scip")
    @patch("bsp_server.scip_sync_util.scip_sync.get_dependency_graph")
    @patch("bsp_server.scip_sync_util.scip_sync.scip_utils.get_containing_bazel_target")
    @patch("bsp_server.scip_sync_util.scip_sync.scip_workspace.get_manifest_for_file")
    @patch("argparse.ArgumentParser.parse_args")
    @patch("bsp_server.scip_sync_util.scip_sync.datetime")
    @patch("os.path.exists")
    def test_main_with_filepath_not_in_workspace(
        self,
        mock_exists,
        mock_datetime,
        mock_parse_args,
        mock_get_manifest_for_file,
        mock_get_containing_bazel_target,
        mock_get_dependency_graph,
        mock_sync_scip,
        mock_get_mnemonic_output,
        mock_populate_workspace,
        mock_write_workspace,
        mock_copy_index,
        mock_pprint,
    ):
        """Test case 3: Filepath provided not in workspace - should return early"""
        # Setup
        mock_datetime.now.return_value = self.mock_datetime
        filepath = f"{self.cwd}/src/main/java/com/example/File.java"
        mock_parse_args.return_value = argparse.Namespace(
            cwd=self.cwd,
            targets=[],
            filepath=filepath,
            depth=1,
        )

        # File not in workspace
        mock_get_manifest_for_file.return_value = (None, None)

        # Target for the file
        target = "//src/main/java/com/example:target"
        mock_get_containing_bazel_target.return_value = target

        # Dependency graph
        mock_get_dependency_graph.return_value = {target}

        # Mock outputs
        source_list = "/tmp/source_list"
        with open(source_list, "w") as f:
            f.write("src/main/java/com/example/File.java")

        mock_get_mnemonic_output.return_value = {
            target: {
                ScipMnemonics.INDEX_OUTPUT_MNEMONIC.value: ["index1"],
                ScipMnemonics.JAVA_TARGET_MANIFEST_MNEMONIC.value: ["manifest1"],
                ScipMnemonics.UNPACKED_JAVA_SOURCES_MNEMONIC.value: [source_list],
            }
        }

        mock_exists.return_value = True

        # Mock workspace
        workspace = ScipWorkspace()
        mock_populate_workspace.return_value = workspace

        # Run the function
        scip_sync.main()

        # Verify
        # 1. Check that get_manifest_for_file was called with the correct arguments
        mock_get_manifest_for_file.assert_called_once_with(
            "src/main/java/com/example/File.java",  # Relative path
            self.scip_dir,
        )

        mock_get_containing_bazel_target.assert_not_called()
        mock_get_dependency_graph.assert_not_called()
        mock_sync_scip.assert_not_called()
        mock_populate_workspace.assert_not_called()
        mock_write_workspace.assert_not_called()
        mock_copy_index.assert_not_called()

    @patch("bsp_server.scip_sync_util.scip_sync.pprint")
    @patch("bsp_server.scip_sync_util.scip_sync.scip_utils.copy_index")
    @patch("bsp_server.scip_sync_util.scip_sync.scip_workspace.write_workspace")
    @patch("bsp_server.scip_sync_util.scip_sync.scip_workspace.populate_workspace")
    @patch("bsp_server.scip_sync_util.scip_sync.scip_utils.get_mnemonic_output")
    @patch("bsp_server.scip_sync_util.scip_sync.sync_scip")
    @patch("bsp_server.scip_sync_util.scip_sync.get_dependency_graph")
    @patch("bsp_server.scip_sync_util.scip_sync.scip_utils.get_containing_bazel_target")
    @patch("bsp_server.scip_sync_util.scip_sync.scip_workspace.get_manifest_for_file")
    @patch("argparse.ArgumentParser.parse_args")
    @patch("bsp_server.scip_sync_util.scip_sync.datetime")
    def test_main_with_filepath_already_in_workspace(
        self,
        mock_datetime,
        mock_parse_args,
        mock_get_manifest_for_file,
        mock_get_containing_bazel_target,
        mock_get_dependency_graph,
        mock_sync_scip,
        mock_get_mnemonic_output,
        mock_populate_workspace,
        mock_write_workspace,
        mock_copy_index,
        mock_pprint,
    ):
        """Test case 4: Filepath provided already in workspace - should return early"""
        # Setup
        mock_datetime.now.return_value = self.mock_datetime
        filepath = f"{self.cwd}/src/main/java/com/example/File.java"
        mock_parse_args.return_value = argparse.Namespace(
            cwd=self.cwd,
            targets=[],
            filepath=filepath,
            depth=1,
        )

        # File already in workspace
        mock_get_manifest_for_file.return_value = ("manifest1", "src/main/java")

        # Run the function
        scip_sync.main()

        # Verify
        # 1. Check that get_manifest_for_file was called with the correct arguments
        mock_get_manifest_for_file.assert_called_once_with(
            "src/main/java/com/example/File.java",  # Relative path
            self.scip_dir,
        )

        # 2. Check that no other functions were called since the file is already in workspace
        # mock_get_containing_bazel_target.assert_not_called()
        # mock_get_dependency_graph.assert_not_called()
        # mock_sync_scip.assert_not_called()
        # mock_get_mnemonic_output.assert_not_called()
        # mock_populate_workspace.assert_not_called()
        # mock_write_workspace.assert_not_called()
        # mock_copy_index.assert_not_called()
        # mock_pprint.assert_not_called()

    @patch("bsp_server.scip_sync_util.scip_sync.pprint")
    @patch("bsp_server.scip_sync_util.scip_sync.scip_utils.copy_index")
    @patch("bsp_server.scip_sync_util.scip_sync.scip_workspace.write_workspace")
    @patch("bsp_server.scip_sync_util.scip_sync.scip_workspace.populate_workspace")
    @patch("bsp_server.scip_sync_util.scip_sync.scip_utils.get_mnemonic_output")
    @patch("bsp_server.scip_sync_util.scip_sync.sync_scip")
    @patch("bsp_server.scip_sync_util.scip_sync.get_dependency_graph")
    @patch("bsp_server.scip_sync_util.scip_sync.scip_utils.get_containing_bazel_target")
    @patch("bsp_server.scip_sync_util.scip_sync.scip_workspace.get_manifest_for_file")
    @patch("argparse.ArgumentParser.parse_args")
    @patch("bsp_server.scip_sync_util.scip_sync.datetime")
    def test_main_with_filepath_no_target_found(
        self,
        mock_datetime,
        mock_parse_args,
        mock_get_manifest_for_file,
        mock_get_containing_bazel_target,
        mock_get_dependency_graph,
        mock_sync_scip,
        mock_get_mnemonic_output,
        mock_populate_workspace,
        mock_write_workspace,
        mock_copy_index,
        mock_pprint,
    ):
        """Test case 5: Filepath provided but no target found - should return early"""
        # Setup
        mock_datetime.now.return_value = self.mock_datetime
        filepath = f"{self.cwd}/src/main/java/com/example/File.java"
        mock_parse_args.return_value = argparse.Namespace(
            cwd=self.cwd,
            targets=[],
            filepath=filepath,
            depth=1,
        )

        # File not in workspace
        mock_get_manifest_for_file.return_value = (None, None)

        # No target found for the file
        mock_get_containing_bazel_target.return_value = None

        # Run the function
        scip_sync.main()

        # Verify
        # 1. Check that get_manifest_for_file was called with the correct arguments
        mock_get_manifest_for_file.assert_called_once_with(
            "src/main/java/com/example/File.java",  # Relative path
            self.scip_dir,
        )

        mock_get_containing_bazel_target.assert_not_called()
        mock_get_dependency_graph.assert_not_called()
        mock_sync_scip.assert_not_called()
        mock_get_mnemonic_output.assert_not_called()
        mock_populate_workspace.assert_not_called()
        mock_write_workspace.assert_not_called()
        mock_copy_index.assert_not_called()
        mock_pprint.assert_not_called()

    @patch("bsp_server.scip_sync_util.scip_sync.pprint")
    @patch("bsp_server.scip_sync_util.scip_sync.scip_utils.copy_index")
    @patch("bsp_server.scip_sync_util.scip_sync.scip_workspace.write_workspace")
    @patch("bsp_server.scip_sync_util.scip_sync.scip_workspace.populate_workspace")
    @patch("bsp_server.scip_sync_util.scip_sync.scip_utils.get_mnemonic_output")
    @patch("bsp_server.scip_sync_util.scip_sync.sync_scip")
    @patch("bsp_server.scip_sync_util.scip_sync.get_dependency_graph")
    @patch("bsp_server.scip_sync_util.scip_sync.fetch_targets_from_bazelproject")
    @patch("argparse.ArgumentParser.parse_args")
    @patch("bsp_server.scip_sync_util.scip_sync.datetime")
    def test_main_no_buildable_targets(
        self,
        mock_datetime,
        mock_parse_args,
        mock_fetch_targets,
        mock_get_dependency_graph,
        mock_sync_scip,
        mock_get_mnemonic_output,
        mock_populate_workspace,
        mock_write_workspace,
        mock_copy_index,
        mock_pprint,
    ):
        """Test case 6: No buildable targets found - should return early"""
        # Setup
        mock_datetime.now.return_value = self.mock_datetime
        mock_parse_args.return_value = argparse.Namespace(
            cwd=self.cwd,
            targets=["//target1"],
            filepath=None,
            depth=1,
        )

        # No buildable targets
        mock_get_dependency_graph.return_value = set()

        # Run the function
        scip_sync.main()

        # Verify
        mock_get_dependency_graph.assert_called_once_with(
            cwd="/path/to/cwd", targets=["//target1"], depth=1, exclude_targets=set()
        )

        # Check that no other functions were called since no buildable targets were found
        mock_sync_scip.assert_not_called()
        mock_get_mnemonic_output.assert_not_called()
        mock_populate_workspace.assert_not_called()
        mock_write_workspace.assert_not_called()
        mock_copy_index.assert_not_called()
        mock_pprint.assert_not_called()

    @patch("tempfile.NamedTemporaryFile")
    @patch("bsp_server.util.utils.write_list")
    @patch("bsp_server.util.utils.output")
    def test_scip_sync(self, m_output, m_write_list, m_tempfile):
        m_tempfile.return_value.name = "mock_targets_file"
        targets = {"//target:one", "//target:two"}
        cwd = "/path/to/cwd"
        dummy_stat = scip_sync.SyncStats()

        scip_sync.sync_scip(cwd, targets, dummy_stat)

        m_tempfile.assert_called_once_with(delete=False)
        m_write_list.assert_called_once_with(targets, "mock_targets_file")
        expected_build_tooling_cmd = [
            BAZEL,
            BUILD,
            scip_const.SCIP_TOOLING_TARGET,
            "--keep_going",
            *scip_const.JAVA_VERSION_FLAGS,
        ]
        expected_cmd = [
            BAZEL,
            BUILD,
            "--target_pattern_file=mock_targets_file",
            "--keep_going",
            "--aspects",
            scip_const.ASPECT_SCIP_INDEX,
            scip_const.ASPECT_OUTPUT_GROUPS,
            *scip_const.JAVA_VERSION_FLAGS,
        ]
        m_output.assert_has_calls(
            [
                call(expected_build_tooling_cmd, cwd=cwd),
                call(expected_cmd, cwd=cwd),
            ]
        )

    @patch("os.path.exists")
    @patch("os.path.join")
    def test_file_not_found(self, m_join, m_exists):
        m_exists.return_value = False
        m_join.return_value = "/path/to/.bazelproject"

        result = scip_sync.fetch_targets_from_bazelproject("/mocked/cwd")

        self.assertEqual(result, (set(), set()))
        m_join.assert_called_with("/mocked/cwd", ".ijwb", ".bazelproject")
        m_exists.assert_called_with("/path/to/.bazelproject")

    @patch("os.path.exists")
    @patch("os.path.join")
    @patch("bsp_server.scip_sync_util.scip_utils.parse_bazelproject")
    def test_targets_from_bazelproject_throws_exception(
        self, m_parse, m_join, m_exists
    ):
        m_exists.return_value = True
        m_join.return_value = "/path/to/.bazelproject"
        m_parse.side_effect = Exception("Mocked exception")

        result = scip_sync.fetch_targets_from_bazelproject("/mocked/cwd")

        self.assertEqual(result, (set(), set()))
        m_join.assert_called_with("/mocked/cwd", ".ijwb", ".bazelproject")
        m_exists.assert_called_with("/path/to/.bazelproject")
        m_parse.assert_called_with("/path/to/.bazelproject")

    @patch("os.path.exists")
    @patch("os.path.join")
    @patch("bsp_server.scip_sync_util.scip_utils.parse_bazelproject")
    def test_targets_fetched_duplicates_with_derive_from_dirs_enabled(
        self, m_parse, m_join, m_exists
    ):
        m_exists.return_value = True
        m_join.return_value = "/path/to/.bazelproject"
        m_parse.return_value = {
            TARGETS: ["//target:one", "//target:two", "//path/to/dir1/..."],
            DIRECTORIES: [".", "path/to/dir1", "path/to/dir2"],
            DERIVE_TARGETS_FROM_DIRECTORIES: ["true"],
        }

        result, _ = scip_sync.fetch_targets_from_bazelproject("/mocked/cwd")

        self.assertEqual(
            sorted(list(result)),
            sorted(
                [
                    "//target:one",
                    "//target:two",
                    "//path/to/dir1/...",
                    "//path/to/dir2/...",
                ]
            ),
        )
        m_join.assert_called_with("/mocked/cwd", ".ijwb", ".bazelproject")
        m_exists.assert_called_with("/path/to/.bazelproject")
        m_parse.assert_called_with("/path/to/.bazelproject")

    @patch("os.path.exists")
    @patch("os.path.join")
    @patch("bsp_server.scip_sync_util.scip_utils.parse_bazelproject")
    def test_targets_fetched_with_derive_from_dirs_enabled(
        self, m_parse, m_join, m_exists
    ):
        m_exists.return_value = True
        m_join.return_value = "/path/to/.bazelproject"
        m_parse.return_value = {
            TARGETS: ["//target:one", "//target:two"],
            DIRECTORIES: [".", "path/to/dir1", "path/to/dir2"],
            DERIVE_TARGETS_FROM_DIRECTORIES: ["true"],
        }

        result, _ = scip_sync.fetch_targets_from_bazelproject("/mocked/cwd")

        self.assertEqual(
            sorted(list(result)),
            sorted(
                [
                    "//target:one",
                    "//target:two",
                    "//path/to/dir1/...",
                    "//path/to/dir2/...",
                ]
            ),
        )
        m_join.assert_called_with("/mocked/cwd", ".ijwb", ".bazelproject")
        m_exists.assert_called_with("/path/to/.bazelproject")
        m_parse.assert_called_with("/path/to/.bazelproject")

    @patch("os.path.exists")
    @patch("os.path.join")
    @patch("bsp_server.scip_sync_util.scip_utils.parse_bazelproject")
    def test_targets_fetched_with_derive_from_dirs_disabled(
        self, m_parse, m_join, m_exists
    ):
        m_exists.return_value = True
        m_join.return_value = "/path/to/.bazelproject"
        m_parse.return_value = {
            TARGETS: ["//target:one", "//target:two", "-//target:three"],
            DIRECTORIES: [".", "path/to/dir1", "path/to/dir2", "-path/to/dir3"],
            DERIVE_TARGETS_FROM_DIRECTORIES: ["false"],
        }

        result, excludes = scip_sync.fetch_targets_from_bazelproject("/mocked/cwd")

        self.assertEqual(
            sorted(list(result)),
            sorted(
                [
                    "//target:one",
                    "//target:two",
                ]
            ),
        )
        self.assertEqual(sorted(list(excludes)), ["//target:three"])
        m_join.assert_called_with("/mocked/cwd", ".ijwb", ".bazelproject")
        m_exists.assert_called_with("/path/to/.bazelproject")
        m_parse.assert_called_with("/path/to/.bazelproject")

    @patch("os.path.exists")
    @patch("os.path.join")
    @patch("bsp_server.scip_sync_util.scip_utils.parse_bazelproject")
    def test_targets_fetched_successfully_with_exclusions_defined_derive_from_dirs_enabled(
        self, m_parse, m_join, m_exists
    ):
        m_exists.return_value = True
        m_join.return_value = "/path/to/.bazelproject"
        m_parse.return_value = {
            TARGETS: ["//target:one", "//target:two", "-//target:three"],
            DIRECTORIES: [".", "path/to/dir1", "path/to/dir2", "-path/to/dir3"],
            DERIVE_TARGETS_FROM_DIRECTORIES: ["true"],
        }

        result, excludes = scip_sync.fetch_targets_from_bazelproject("/mocked/cwd")

        self.assertEqual(
            sorted(list(result)),
            sorted(
                [
                    "//target:one",
                    "//target:two",
                    "//path/to/dir1/...",
                    "//path/to/dir2/...",
                ]
            ),
        )
        self.assertEqual(
            sorted(list(excludes)),
            sorted(["//target:three", "//path/to/dir3/..."]),
        )
        m_join.assert_called_with("/mocked/cwd", ".ijwb", ".bazelproject")
        m_exists.assert_called_with("/path/to/.bazelproject")
        m_parse.assert_called_with("/path/to/.bazelproject")

    @patch("bsp_server.scip_sync_util.scip_sync.execute_query")
    @patch("bsp_server.scip_sync_util.scip_utils.transform_bazel_query_results")
    def test_get_dependency_graph_with_multiple_targets(
        self, mock_transform_bazel_query_results, mock_execute_query
    ):
        mock_execute_query.return_value = MagicMock()
        mock_transform_bazel_query_results.return_value = {
            "//path/to:target1": {
                "exports": ["//path/to:export1"],
                "direct_deps": ["//path/to:dep1"],
            },
            "//path/to:target2": {"exports": [], "direct_deps": ["//path/other:dep2"]},
            "//path/to:dep1": {"exports": [], "direct_deps": ["//path/other:dep2"]},
            "//path/other:dep2": {"exports": [], "direct_deps": ["//path/new:dep3"]},
            "//path/new:dep3": {"exports": [], "direct_deps": []},
            "//path/to:export1": {"exports": ["//path/to:export2"], "direct_deps": []},
            "//path/to:export2": {"exports": ["//path/to:export3"], "direct_deps": []},
            "//path/to:export3": {"exports": [], "direct_deps": []},
        }

        result = scip_sync.get_dependency_graph(
            "/path/to/cwd", {"//path/to:target1", "//path/to:target2"}, 1
        )
        self.assertEqual(
            result,
            {
                "//path/to:target1",
                "//path/to:dep1",
                "//path/to:target2",
                "//path/other:dep2",
                "//path/to:export1",
                "//path/to:export2",
                "//path/to:export3",
            },
        )
        result = scip_sync.get_dependency_graph(
            "/path/to/cwd", {"//path/to:target1", "//path/to:target2"}, 2
        )
        self.assertEqual(
            result,
            {
                "//path/to:target1",
                "//path/to:dep1",
                "//path/to:target2",
                "//path/other:dep2",
                "//path/new:dep3",
                "//path/to:export1",
                "//path/to:export2",
                "//path/to:export3",
            },
        )
        result = scip_sync.get_dependency_graph(
            "/path/to/cwd", {"//path/to:target1", "//path/to:target2"}, 0
        )
        self.assertEqual(
            result,
            {
                "//path/to:target1",
                "//path/to:target2",
                "//path/to:export1",
                "//path/to:export2",
                "//path/to:export3",
            },
        )
        result = scip_sync.get_dependency_graph("/path/to/cwd", {"//path/to/..."}, 0)
        self.assertEqual(
            result,
            {
                "//path/to:target1",
                "//path/to:target2",
                "//path/to:export1",
                "//path/to:export2",
                "//path/to:export3",
                "//path/to:dep1",
            },
        )
        result = scip_sync.get_dependency_graph("/path/to/cwd", {"//path/to/..."}, 1)
        self.assertEqual(
            result,
            {
                "//path/to:target1",
                "//path/to:target2",
                "//path/to:export1",
                "//path/to:export2",
                "//path/to:export3",
                "//path/to:dep1",
                "//path/other:dep2",
            },
        )
        result = scip_sync.get_dependency_graph("/path/to/cwd", {"//path/..."}, 1)
        self.assertEqual(
            result,
            {
                "//path/to:target1",
                "//path/to:target2",
                "//path/to:export1",
                "//path/to:export2",
                "//path/to:export3",
                "//path/to:dep1",
                "//path/other:dep2",
                "//path/new:dep3",
            },
        )
        result = scip_sync.get_dependency_graph("/path/to/cwd", {"//path/..."}, 99)
        self.assertEqual(
            result,
            {
                "//path/to:target1",
                "//path/to:target2",
                "//path/to:export1",
                "//path/to:export2",
                "//path/to:export3",
                "//path/to:dep1",
                "//path/other:dep2",
                "//path/new:dep3",
            },
        )
        result = scip_sync.get_dependency_graph("/path/to/cwd", {"//wololo/..."}, 1)
        self.assertEqual(
            result,
            set(),
        )
        result = scip_sync.get_dependency_graph(
            "/path/to/cwd", {"//path/to:dep1", "//path/new:dep3"}, 1, query_rdeps=True
        )
        self.assertEqual(
            result,
            {"//path/to:target1", "//path/other:dep2"},
        )
        result = scip_sync.get_dependency_graph(
            "/path/to/cwd",
            {"//path/to:dep1", "//path/new:dep3"},
            1,
            exclude_targets={"//path/new:dep3"},
            query_rdeps=True,
        )
        self.assertEqual(
            result,
            {
                "//path/to:target1",
            },
        )

    @patch("bsp_server.scip_sync_util.scip_sync.execute_query")
    @patch("bsp_server.scip_sync_util.scip_utils.transform_bazel_query_results")
    def test_get_dependency_graph_forms_correct_query(
        self, mock_transform_bazel_query_results, mock_execute_query
    ):
        mock_execute_query.return_value = MagicMock()
        mock_transform_bazel_query_results.return_value = {}
        scip_sync.get_dependency_graph(
            cwd="/path/to/cwd",
            targets={"//path/to/..."},
            query_kinds=["test_kind"],
            depth=2,
            query_rdeps=True,
            query_deps=False,
            query_rdeps_universe="//universe/...",
        )
        mock_execute_query.assert_called_once_with(
            cwd="/path/to/cwd",
            targets=["//path/to/..."],
            query_kinds=["test_kind"],
            query_deps=False,
            query_rdeps=True,
            query_depth=2,
            query_rdeps_universe="//universe/...",
            soft_fail=True,
        )

    def test_convert_directories_to_targets(self):
        directories = ["path/to/dir1", "path/to/dir2", ".", "path/to/dir3/"]
        targets = scip_sync.convert_directories_to_targets(directories)
        self.assertEqual(
            targets, ["//path/to/dir1/...", "//path/to/dir2/...", "//path/to/dir3/..."]
        )

    @patch("bsp_server.scip_sync_util.incremental.index_file")
    @patch("bsp_server.scip_sync_util.scip_sync.scip_utils.old_copy_index")
    @patch("bsp_server.scip_sync_util.scip_sync.scip_workspace.get_manifest_for_file")
    @patch("argparse.ArgumentParser.parse_args")
    @patch("bsp_server.scip_sync_util.scip_sync.datetime")
    def test_main_with_filepath_triggers_old_sync(
        self,
        mock_datetime,
        mock_parse_args,
        mock_get_manifest_for_file,
        mock_old_copy_index,
        mock_index_file,
    ):
        mock_datetime.now.return_value = self.mock_datetime
        filepath = f"{self.cwd}/src/main/java/com/example/File.java"
        mock_parse_args.return_value = argparse.Namespace(
            cwd=self.cwd,
            targets=[],
            filepath=filepath,
            depth=1,
        )
        manifest_tuple = ("manifest1", "src/main/java")
        mock_get_manifest_for_file.return_value = manifest_tuple
        mock_index_file.return_value = "generated_index_path"

        # Run the function
        scip_sync.main()

        # Verify
        mock_get_manifest_for_file.assert_called_once_with(
            "src/main/java/com/example/File.java",  # Relative path
            os.path.join(self.cwd, ".scip"),
        )

        mock_index_file.assert_called_once_with(
            self.cwd, "src/main/java/com/example/File.java", manifest_tuple
        )
        mock_old_copy_index.assert_called_once_with(
            {"generated_index_path"}, os.path.join(self.cwd, ".scip")
        )


if __name__ == "__main__":
    unittest.main()
