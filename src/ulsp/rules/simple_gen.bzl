load("@rules_go//go:def.bzl", "GoArchive", "GoInfo", "go_context", "new_go_info")

def _resolve(go, attr, go_info, merge):
    """Simple resolver that adds default deps"""
    transitive = []
    for dep in go_info["deps"]:
        tdeps = dep.direct
        transitive += tdeps
        for tdep in tdeps:
            transitive += tdep.direct
    go_info["deps"] += transitive + [d[GoArchive] for d in attr._default_deps]

def _simple_gen_impl(ctx):
    """Generate a simple Go file with a function"""
    go = go_context(ctx, importpath = ctx.attr.importpath)
    
    # Generate a simple Go source file
    out = go.declare_file(go, "{}.go".format(ctx.label.name))
    
    content = """package {}

import "go.uber.org/fx"

// GeneratedModule returns an fx.Option for the generated code
func GeneratedModule() fx.Option {{
    return fx.Options()
}}
""".format(ctx.attr.package_name)
    
    ctx.actions.write(
        output = out,
        content = content,
    )
    
    # Create GoInfo similar to glue rules
    go_info = new_go_info(
        go,
        ctx.attr,
        resolver = _resolve,
        generated_srcs = [out],
        coverage_instrumented = ctx.coverage_instrumented(),
    )
    archive = go.archive(go, go_info)
    
    return [
        go_info,
        archive,
        DefaultInfo(
            files = depset([archive.data.file]),
            runfiles = archive.runfiles,
        ),
        OutputGroupInfo(
            compilation_outputs = [archive.data.file],
            go_generated_srcs = [out],
        ),
    ]

simple_gen = rule(
    implementation = _simple_gen_impl,
    attrs = {
        "deps": attr.label_list(
            providers = [GoInfo],
        ),
        "importpath": attr.string(
            mandatory = True,
        ),
        "package_name": attr.string(
            mandatory = True,
        ),
        "_default_deps": attr.label_list(
            default = [
                "@org_uber_go_fx//:go_default_library",
            ],
        ),
        "_go_context_data": attr.label(default = "@rules_go//:go_context_data"),
    },
    toolchains = ["@rules_go//go:toolchain"],
)

