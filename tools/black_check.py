#!/usr/bin/env python3
"""Check Python code formatting with black."""

import os
import sys

from black import main as black_main


def main():
    print("Checking Python code formatting with black...")

    # Change to workspace root if we're in a Bazel execroot
    if "execroot" in os.getcwd():
        workspace_root = os.environ.get(
            "BUILD_WORKSPACE_DIRECTORY", "/home/user/sources/scip-lsp"
        )
        os.chdir(workspace_root)
        print(f"Changed to workspace directory: {workspace_root}")

    # Set up black arguments for check mode
    black_args = ["--check", "--diff", "bsp_server", "tools"]

    # Temporarily replace sys.argv to pass arguments to black
    original_argv = sys.argv
    sys.argv = ["black"] + black_args

    try:
        black_main()
        print("All Python files are properly formatted ✓")
        return 0
    except SystemExit as e:
        if e.code == 0:
            print("All Python files are properly formatted ✓")
            return 0
        else:
            print("Python files need formatting")
            print("\nRun 'bazel run //tools:black_fix' to fix formatting")
            return 1
    except Exception as e:
        print(f"Error running black: {e}")
        return 1
    finally:
        # Restore original sys.argv
        sys.argv = original_argv


if __name__ == "__main__":
    sys.exit(main())
