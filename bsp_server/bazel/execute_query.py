import json
import tempfile
from typing import Optional

from tqdm import tqdm

from bsp_server.util import utils

SRCS = "srcs"
TAGS = "tags"

TARGET_PROTO_ATTRIBUTES_ALLOW_LIST = {SRCS, TAGS}
EXTERNAL_REPO_PREFIX = "@"


def execute_query(
    cwd: str,
    targets: list[str],
    query_kinds: Optional[list[str]] = None,
    query_deps: bool = False,
    query_rdeps: bool = False,
    query_rdeps_universe: Optional[str] = None,
    query_depth: Optional[int] = None,
    query_filter: Optional[str] = None,
    stderr=None,
    env_vars=None,
    soft_fail=False,
) -> list[dict]:
    """
    Execute a bazel query to gather information from each of the targets in the
    'targets parameter'. We deserialize the results into json objects for further
    processing. Only targets of type "RULE" is included. Also, target names with
    external repo prefix is excluded.

    :param cwd: the current working directory to execute the query
    :param targets: the targets to query information about
    :param query_kinds: rules names to query
    :param query_deps: whether to query deps of those targets or not. It will
    end up querying `external` targets and can be very slow to run. If you don't
    need external targets query targets using //... and manually filter out
    unwanted targets.
    :param query_rdeps: whether to query rdeps of those targets or not.
    :param query_rdeps_universe: universe for rdeps query
    :param query_depth: depth of the deps or rdeps retrieved by query.
    If not specified query is unbounded.
    :param stderr: location to redirect the error stream
    :param env_vars: any environment variables to pass to the process
    executing the query
    :param query_filter: An optional filter expression passed to Bazel query.
    :param soft_fail: Ignore query errors
    :return: a QueryResult containing the results of the query.
    """
    query_file = _create_query_file(
        targets=targets,
        query_kinds=query_kinds,
        query_deps=query_deps,
        query_rdeps=query_rdeps,
        query_depth=query_depth,
        query_rdeps_universe=query_rdeps_universe,
        query_filter=query_filter,
    )

    cmd = [
        "bazelisk",
        "query",
        "--output",
        "streamed_jsonproto",
        "--order_output=no",
        "--query_file",
        query_file,
    ]
    if soft_fail:
        cmd.append("--keep_going")

    outputs = []
    query_return_code = 0
    query_error_string = None
    try:
        for return_code, error_string, line in tqdm(
            utils.stream_output(
                command=cmd,
                cwd=cwd,
                stderr=stderr,
                env_vars=env_vars,
            ),
            desc="Running bazel query (approx 45s)",
            unit=" targets",
        ):
            query_return_code = return_code
            query_error_string = error_string
            try:
                outputs.append(json.loads(line))
            finally:
                continue

    finally:
        utils.safe_delete(query_file)

    if not soft_fail and query_return_code != 0:
        raise RuntimeError(
            f"Bazel query failed with return code: "
            f"{query_return_code} and error: {query_error_string}",
        )

    return outputs


def _create_query_file(
    targets: list[str],
    query_kinds: Optional[list[str]] = None,
    query_deps: bool = False,
    query_rdeps: bool = False,
    query_tags: Optional[list[str]] = None,
    query_depth: Optional[int] = None,
    query_rdeps_universe: Optional[str] = None,
    query_filter: Optional[str] = None,
) -> str:
    """
    Bazel has a maximum number of arguments:
    https://github.com/bazelbuild/bazel/issues/8609
    If a diff has many targets, we may hit this limit when specifying which
    targets to query one-by-one. We can get around this limitation by using
    a query file instead.

    :param targets: The targets we want to include in the query
    :param query_kinds: kind of rules to keep
    :param query_deps: whether to get deps
    :param query_rdeps: whether to get rdeps
    :param query_tags: tags to filter the targets.
    :param query_depth: depth of the deps or rdeps
    :param query_rdeps_universe: universe for rdeps
    :param query_filter: An optional filter expression passed to Bazel query
    :return: the name of the query file
    """
    query_file = tempfile.NamedTemporaryFile(delete=False).name
    utils.write_string_content(
        _query_string(
            targets=targets,
            query_kinds=query_kinds,
            query_deps=query_deps,
            query_rdeps=query_rdeps,
            query_tags=query_tags,
            query_depth=query_depth,
            query_rdeps_universe=query_rdeps_universe,
            query_filter=query_filter,
        ),
        query_file,
    )
    return query_file


def _query_string(
    targets: list[str],
    query_kinds: Optional[list[str]] = None,
    query_deps: bool = False,
    query_rdeps: bool = False,
    query_depth: Optional[int] = None,
    query_tags: Optional[list[str]] = None,
    query_rdeps_universe: Optional[str] = None,
    query_filter: Optional[str] = None,
) -> str:
    """
    To query multiple targets by name, we take the union of them. For example,
    if we want to query for targets A, B, and C, we would query with:

    bazel query "A" + "B + "C"

    If query_deps is enabled then the query would look like

    bazel query deps("A" + "B" + "C")

    This method takes in a list of targets and outputs the args to pass to bazel
    query by injecting a "union" between each target name.

    :param targets: A list of targets to query
    :param query_tags: tags to filter the targets.
    :param query_kinds: kind of rules to keep
    :param query_deps: whether to get deps
    :param query_rdeps: whether to get rdeps
    :param query_rdeps_universe: universe for rdeps
    :param query_depth: depth of the deps or rdeps
    :param query_filter: An optional filter expression passed to Bazel query
    :return: raw string to pass to bazel query
    """
    if not targets:
        raise RuntimeError("targets passed to _union must not be empty.")

    if query_deps and query_rdeps:
        raise RuntimeError("query_deps and query_rdeps cannot be used together.")

    query_string = " + ".join([f'"{target}"' for target in targets])

    if query_deps or query_rdeps:
        if query_rdeps:
            query_fun = "rdeps"
            universe = query_rdeps_universe if query_rdeps_universe else "//..."
            query_string = f'"{universe}", {query_string}'
        else:
            query_fun = "deps"

        if query_depth is None:
            query_string = f"{query_fun}({query_string})"
        else:
            query_string = f"{query_fun}({query_string}, {query_depth})"

    if query_tags:
        pattern = "|".join([f"\\b{tag}\\b" for tag in query_tags])
        query_string = f'attr(tags, "{pattern}", {query_string})'

    if query_kinds:
        query_string = f'kind("{"|".join(query_kinds)}", {query_string})'

    # Apply bazel query filter functionality if query_filter is provided
    if query_filter:
        query_string = f'filter(".*{query_filter}.*", {query_string})'

    return query_string
