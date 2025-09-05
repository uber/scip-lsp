"""
Bazel aspect to run scip-java against a Java Bazel codebase.

    bazel build 3rdparty/jvm/com/google/api:api-common-2.8.0.jar --aspects //tools/scip/index:scip.bzl%scip_java_aspect --output_groups=scip

    Decompile target:

    bazel build 3rdparty/jvm/com/uber/devxp:unsafe-compat-0.0.2.jar --aspects //tools/scip/index:scip.bzl%scip_java_aspect --output_groups=scip

    bazel build //tools/scip/aggregator/... --aspects //tools/scip/index:scip.bzl%scip_java_aspect --output_groups=scip

    No java files

    bazel build //3rdparty/jvm/org/bytedeco:hdf5-1.12.0-1.5.3-linux-x86_64.jar --aspects //tools/scip/index:scip.bzl%scip_java_aspect --output_groups=scip

    bazel build //3rdparty/jvm/com/sun:tools-1.8.0-141.jar --aspects //tools/scip/index:scip.bzl%scip_java_aspect --output_groups=scip
    bazel build //experimental/users/hshukla/scip/java-sample:src_main --aspects //tools/scip/index:scip.bzl%scip_java_aspect --output_groups=scip
To learn more about aspects: https://bazel.build/extending/aspects
"""

load("@rules_java//java/common:java_common.bzl", "java_common")
load("@rules_java//java/common:java_info.bzl", "JavaInfo")

MAX_ALLOWED_FILES_IN_JAR = 4500

def _scip_java(target, ctx):
    decompiler = ctx.executable._decompiler

    # Java toolchain info
    java_home = ctx.attr._java_toolchain[java_common.JavaToolchainInfo].java_runtime.java_home
    java_runtime = ctx.attr._java_toolchain[java_common.JavaToolchainInfo].java_runtime

    if JavaInfo not in target:
        return None

    unsupported_tags = ["shaded_binary", "no-ide"]
    if _rule_contains_unsupported_tags(ctx, unsupported_tags):
        return None

    # sources
    rule_sources = getattr(ctx.rule.files, "srcs", [])

    # jars
    rule_jars = getattr(ctx.rule.files, "jars", [])

    # source jars
    rule_source_jars = getattr(ctx.rule.files, "srcjar", [])

    scips = []
    generated_source_jars = []

    if rule_sources:
        javac_action = None
        for a in target.actions:
            if a.mnemonic == "Javac":
                javac_action = a
                if hasattr(target[JavaInfo], "annotation_processing") and hasattr(target[JavaInfo].annotation_processing, "source_jar") and target[JavaInfo].annotation_processing.source_jar:
                    generated_source_jars.append(target[JavaInfo].annotation_processing.source_jar)
                break

        if generated_source_jars:
            for index, generated_source_jar in enumerate(generated_source_jars):
                scips.append(_index_jar(ctx = ctx, target = target, jar = generated_source_jar, flow_prefix = "_annotation_processor_generated_sources_" + str(index)))

        source_files = []
        source_jars = []

        # set is not supported, thus we need dict
        source_roots = {}

        for src in ctx.rule.files.srcs:
            if src.path.endswith(".java") and not src.path.endswith("/package-info.java") and src.path.find("META-INF/") == -1:
                source_files.append(src.path)
                dirname = src.dirname

                # Trim src/main/java since we will need to do classpath lookup
                # TODO(andriid): check if we will need separate outgroup later for this to speed up generation
                if "src/main/java" in dirname:
                    index = dirname.index("src/main/java") + len("src/main/java")
                    dirname = dirname[:index]
                elif "src/test/java" in dirname:
                    index = dirname.index("src/test/java") + len("src/test/java")
                    dirname = dirname[:index]
                source_roots.setdefault(dirname, default = None)
            elif src.path.endswith(".srcjar"):
                source_jars.append(src)

        if len(source_jars):
            for source_jar in source_jars:
                scips.append(_index_jar(ctx = ctx, target = target, jar = source_jar))

        if len(source_files):
            sources_file = ctx.actions.declare_file(ctx.label.name + "_sources.txt")
            ctx.actions.run_shell(
                command = """
                echo "{files}" > "{sources_file}";
                sort -o "{sources_file}" "{sources_file}";
                touch -t 197001010000.00 "{sources_file}";
                """.format(
                    files = "\n".join(source_files),
                    sources_file = sources_file.path,
                ),
                env = {
                },
                progress_message = "Listing java files from %s" % ctx.label.name,
                mnemonic = "scipFindUnpackedJavaSources",
                inputs = depset(ctx.rule.files.srcs),
                outputs = [sources_file],
            )
            scips.append(
                _index_sources(
                    ctx = ctx,
                    target = target,
                    sources_file = sources_file,
                    additional_classpath = source_roots.keys(),
                    inputs = javac_action.inputs,
                ),
            )

    elif rule_jars or rule_source_jars:
        if rule_source_jars:
            for source_jar in rule_source_jars:
                scips.append(_index_jar(ctx = ctx, target = target, jar = source_jar))
        else:
            timeout = ctx.var.get("decompiler_timeout", "160")
            max_files = ctx.var.get("max_files", str(MAX_ALLOWED_FILES_IN_JAR))
            for jar in rule_jars:
                # Mimic arguments from java decompiler plugin in Intellij
                # mirror: https://github.com/fesh0r/fernflower
                decompiled_jar = ctx.actions.declare_file("out/" + jar.short_path + "_dec.jar")
                args = [
                    "-target_file=" + decompiled_jar.path,
                    "-timeout=" + timeout,
                    "-max_files=" + max_files,
                    "-hdc=0",
                    "-dgs=1",
                    "-rsy=1",
                    "-rbr=1",
                    "-mpm=60",
                    "-iib=1",
                    "-vac=1",
                    "-cps=1",
                    "-crp=1",
                    "-log=ERROR",
                    jar.path,
                ]
                ctx.actions.run(
                    arguments = args,
                    progress_message = "Decompiling provided jar for %s" % ctx.label.name,
                    executable = decompiler,
                    tools = [java_runtime.files],
                    mnemonic = "scipDecompileJavaSources",
                    inputs = depset([jar]),
                    outputs = [decompiled_jar],
                    env = {
                        "JAVA_HOME": java_home,
                    },
                )
                scips.append(_index_jar(ctx = ctx, target = target, jar = decompiled_jar))
    return scips

def _index_sources(
        ctx,
        target,
        sources_file,
        sources_folders = [],
        inputs = depset(),
        additional_classpath = [],
        flow_prefix = "_index_sources"):
    target_name = ctx.label.package + ":" + ctx.label.name
    classpath = _get_classpath_from_target(ctx, target, flow_prefix)
    indexer = ctx.executable._java_aggregate_binary
    sematicdb_javac_plugin = ctx.attr._javac_semanticdb_plugin[DefaultInfo].files.to_list()[0]

    options_file = ctx.actions.declare_file(ctx.label.name + flow_prefix + "_options")

    classpath = classpath + [sematicdb_javac_plugin]
    classpath_line = ":".join([dep.path for dep in classpath] + additional_classpath)
    scip_file_mutated_label = ctx.label.name + flow_prefix + "_index_mutated.scip"
    scip_file_mutated = ctx.actions.declare_file(scip_file_mutated_label)

    ctx.actions.expand_template(
        template = ctx.file._java_aggregate_binary_config_template,
        output = options_file,
        substitutions = {
            "{CLASSPATH}": classpath_line,
            "{FILES_LIST}": sources_file.path,
            "{OUTPUT}": scip_file_mutated.path,
            "{SEMANTICDB_PLUGIN}": sematicdb_javac_plugin.path,
        },
    )

    action_inputs = depset(sources_folders + [options_file, sources_file] + classpath, transitive = [inputs])

    ctx.actions.run(
        arguments = ["-m", options_file.path],
        progress_message = "Scip generation for %s" % ctx.label.package + ":" + ctx.label.name,
        executable = indexer,
        mnemonic = "scipMutation",
        inputs = action_inputs,
        env = {
            "JAVA_OPTS": "-Xmx2g",
        },
        outputs = [scip_file_mutated],
    )

    # Generate SHA256 hash file
    sha256_file = ctx.actions.declare_file(scip_file_mutated_label + ".sha256")
    ctx.actions.run_shell(
        inputs = [scip_file_mutated],
        outputs = [sha256_file],
        command = "shasum -a 256 {} | cut -d' ' -f1 > {}".format(
            scip_file_mutated.path,
            sha256_file.path,
        ),
        mnemonic = "scipSHA256",
    )

    return [scip_file_mutated, sha256_file]

def _index_jar(ctx, target, jar, flow_prefix = "_index_jar"):
    unpacked = ctx.actions.declare_directory("out/" + jar.short_path + flow_prefix + "-unpacked")
    ctx.actions.run_shell(
        inputs = [jar],
        outputs = [unpacked],
        mnemonic = "scipExtractJar",
        command = """
            unzip {input_file} -d {output_dir} 1>/dev/null;
            find {output_dir} -exec touch -t 197001010000.00 {{}} +;
        """.format(
            output_dir = unpacked.path,
            input_file = jar.path,
        ),
        progress_message = "Extracting jar {jar}".format(jar = jar.path),
    )

    sources_file = ctx.actions.declare_file("out/" + jar.short_path + "_sources.txt")
    ctx.actions.run_shell(
        command = """
            touch "{sources_file}";
            find_output=$(find "{unpacked}" -name "*.java" ! -name "module-info.java" ! -name 'XXXXXX.java' ! -path "*/META-INF/*");
            count=$(echo "$find_output" | wc -l);
            if [ "$count" -le {max_files} ]; then
                echo "$find_output" >> "{sources_file}";
            fi
            sort -o "{sources_file}" "{sources_file}";
            touch -t 197001010000.00 "{sources_file}";
        """.format(
            unpacked = unpacked.path,
            sources_file = sources_file.path,
            max_files = str(MAX_ALLOWED_FILES_IN_JAR),
        ),
        env = {
        },
        progress_message = "Listing java files from %s" % ctx.label.name,
        mnemonic = "scipFindUnpackedJavaSources",
        inputs = depset([unpacked]),
        outputs = [sources_file],
    )
    return _index_sources(
        ctx = ctx,
        target = target,
        sources_file = sources_file,
        sources_folders = [unpacked],
        flow_prefix = flow_prefix,
    )

def _scip_java_aspect(target, ctx):
    scips = _scip_java(target, ctx)
    if not scips:
        return struct()

    flat_files = []
    for output in scips:
        if type(output) == type([]):
            flat_files.extend(output)
        else:
            flat_files.append(output)
    return [OutputGroupInfo(scip = flat_files)]

scip_java_aspect = aspect(
    _scip_java_aspect,
    attrs = {
        "_javac_semanticdb_plugin": attr.label(
            default = Label("@maven//:com_sourcegraph_semanticdb_javac"),
            cfg = "exec",
        ),
        "_lombok_extractor": attr.label(
            default = Label("@scip_lsp//src/main/java/com/uber/scip/extractor:extractor_bin"),
            executable = True,
            cfg = "exec",
        ),
        "_decompiler": attr.label(
            default = Label("@scip_lsp//src/main/java/com/uber/intellij/jd:decompiler_bin"),
            executable = True,
            cfg = "exec",
        ),
        "_java_toolchain": attr.label(
            default = Label("@rules_java//toolchains:current_java_toolchain"),
            cfg = "exec",
        ),
        "_java_aggregate_binary": attr.label(
            default = Label("@scip_lsp//src/main/java/com/uber/scip/aggregator:aggregator_bin"),
            executable = True,
            cfg = "exec",
        ),
        "_java_aggregate_binary_config_template": attr.label(
            default = ":config.template",
            allow_single_file = True,
        ),
    },
)

def _get_classpath_from_target(ctx, target, flow_prefix):
    info = target[JavaInfo]
    compilation = info.compilation_info

    # compilation_info can be None for scala library/test targets
    # In this case we rely on JavaInfo compile/runtime atrributes
    compilation_info = info.compilation_info

    # We have to include gen sources to the compilation classpath
    generated_class_jars = []
    for a in target.actions:
        if a.mnemonic == "Javac":
            javac_action = a
            if hasattr(target[JavaInfo], "annotation_processing") and hasattr(target[JavaInfo].annotation_processing, "class_jar") and target[JavaInfo].annotation_processing.class_jar:
                generated_class_jars.append(target[JavaInfo].annotation_processing.class_jar)

    if compilation_info == None:
        return info.transitive_compile_time_jars.to_list()
    else:
        lombok_extractor = ctx.executable._lombok_extractor

        # First jar of the runtime_classpath is the generated jar for the target, it will contain
        # all the lombok generated classes. We need to extract them since they not added as separate
        # output of the javac action.
        generated_final_jar = compilation_info.runtime_classpath.to_list()[0]
        lombok_classes_jar = ctx.actions.declare_file(ctx.label.name + flow_prefix + "_gen_lombok.jar")
        args = [generated_final_jar.path, lombok_classes_jar.path]
        ctx.actions.run(
            arguments = args,
            progress_message = "Extracting lombok classes for %s" % ctx.label.name,
            executable = lombok_extractor,
            mnemonic = "scipGetLombokGenClasses",
            inputs = depset([generated_final_jar]),
            outputs = [lombok_classes_jar],
            env = {
                "JAVA_OPTS": "-Xmx2g",
            },
        )

        return [lombok_classes_jar] + generated_class_jars + compilation_info.compilation_classpath.to_list()

def _scip_java_impl(ctx):
    output = ctx.attr.compilation[OutputGroupInfo]
    return [
        OutputGroupInfo(scip = output.scip),
        DefaultInfo(files = output.scip),
    ]

def _rule_contains_unsupported_tags(ctx, tags):
    if not hasattr(ctx.rule.attr, "tags"):
        return False

    for tag in tags:
        if tag in ctx.rule.attr.tags:
            return True
    return False

scip_java = rule(
    implementation = _scip_java_impl,
    attrs = {
        "compilation": attr.label(aspects = [scip_java_aspect]),
    },
)
