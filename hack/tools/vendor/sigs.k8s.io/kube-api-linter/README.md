# Kube API Linter

Kube API Linter (KAL) is a Golang based linter for Kubernetes API types.
It checks for common mistakes and enforces best practices.
The rules implemented by the Kube API Linter, are based on the [Kubernetes API Conventions][api-conventions].

Kube API Linter is aimed at being an assistant to API review, by catching the mechanical elements of API review, and allowing reviewers to focus on the more complex aspects of API design.

[api-conventions]: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md

## Installation

Kube API Linter ships as a standalone binary, golangci-lint plugin, and a golangci-lint module.

### Standalone binary

The binary version of Kube API Linter can be built with `make build` or a standard `go build` command.
```bash
go build -o ./bin ./cmd/golangci-lint-kube-api-linter 
```

The binary builds a custom version of `golangci-lint` with Kube API Linter included as a module.
See [Golangci-lint Module](#golangci-lint-module) for details on configuration of the module
under `settings`.

### Golangci-lint Module

To install the `golangci-lint` module, first you must have `golangci-lint` v2 installed.
If you do not have `golangci-lint` installed, review the `golangci-lint` [install guide][golangci-lint-install].

[golangci-lint-install]: https://golangci-lint.run/welcome/install/

You will need to create a `.custom-gcl.yml` file to describe the custom linters you want to run. The following is an example of a `.custom-gcl.yml` file:

```yaml
version: v2.5.0
name: golangci-lint-kube-api-linter
destination: ./bin
plugins:
- module: 'sigs.k8s.io/kube-api-linter'
  version: 'v0.0.0-20251029102002-9992248f8813'
```

**Important - Version Format**: Since this repository does not have releases yet, you must use a [pseudo-version](https://go.dev/ref/mod#pseudo-versions) in the format `v0.0.0-YYYYMMDDHHMMSS-commithash`.

To find the latest version listed, check [pkg.go.dev/sigs.k8s.io/kube-api-linter?tab=versions](https://pkg.go.dev/sigs.k8s.io/kube-api-linter?tab=versions)

Once you have created the custom configuration file, you can run the following command to build the custom binary:

```shell
golangci-lint custom
```

The output binary will be created at the location specified by the `destination` field in `.custom-gcl.yml` and will be a combination of the `golangci-lint` binary with the Kube API Linter included as a module.

This means you can use any of the standard `golangci-lint` configuration or flags to run the binary, with the addition of the Kube API Linter rules.

If you wish to only use the Kube API Linter rules, you can configure your `.golangci.yml` file to only run the Kube API Linter:

```yaml
version: "2"

linters:
  default: none
  enable:
    - kubeapilinter

  settings:
    custom:
      kubeapilinter:
        type: module
        description: Kube API Linter lints Kube like APIs based on API conventions and best practices.
        settings:
          linters: {}
          lintersConfig: {}

  # To only run Kube API Linter on specific path
  exclusions:
    rules:
      - linters:
          - kubeapilinter
        path-except: api/*

```

If you wish to only run selected linters you can do so by specifying the linters you want to enable in the `linters` section:

```yaml
version: "2"

linters:
  enable:
    - kubeapilinter

  settings:
    custom:
      kubeapilinter:
        type: "module"
        settings:
          linters:
            disable:
              - "*"
            enable:
              - requiredfields
              - statusoptional
              - statussubresource
```

The settings for Kube API Linter are based on the [GolangCIConfig][golangci-config-struct] struct and allow for finer control over the linter rules.

If you wish to use the Kube API Linter in conjunction with other linters, you can enable the Kube API Linter in the `.golangci.yml` file by ensuring that `kubeapilinter` is in the `linters.enabled` list.
To provide further configuration, add the `custom.kubeapilinter` section to your `settings` as per the example above.

[golangci-config-struct]: https://pkg.go.dev/sigs.k8s.io/kube-api-linter/pkg/config#GolangCIConfig

Where fixes are available within a rule, these can be applied automatically with the `--fix` flag:

```shell
golangci-lint-kube-api-linter run path/to/api/types --fix
```

### Golangci-lint Plugin

The Kube API Linter can also be used as a plugin for `golangci-lint`.
To do this, you will need to install the `golangci-lint` binary and then install the Kube API Linter plugin.

More information about golangci-lint plugins can be found in the [golangci-lint plugin documentation][golangci-lint-plugin-docs].

[golangci-lint-plugin-docs]: https://golangci-lint.run/plugins/go-plugins/

To build the plugin, use the `-buildmode=plugin` flag:

```shell
go build -buildmode=plugin -o bin/kube-api-linter.so sigs.k8s.io/kube-api-linter/pkg/plugin
```

**Note**: If you're building the plugin from within another project that vendors kube-api-linter, use the vendor path:

```shell
go build -mod=vendor -buildmode=plugin -o bin/kube-api-linter.so ./vendor/sigs.k8s.io/kube-api-linter/pkg/plugin
```

This will create a `kube-api-linter.so` plugin file in the specified directory.

The `golangci-lint` configuration is similar to the module configuration, however, you will need to specify the plugin path instead in your `.golangci.yml`:

```yaml
version: "2"

linters:
  enable:
    - kubeapilinter

  settings:
    custom:
      kubeapilinter:
        path: "bin/kube-api-linter.so"
        description: Kube API Linter lints Kube like APIs based on API conventions and best practices.
        original-url: sigs.k8s.io/kube-api-linter
        settings:
          linters: {}
          lintersConfig: {}
```

The rest of the configuration is the same as the module configuration, except the standard `golangci-lint` binary is invoked, rather than a custom binary.

#### VSCode integration

Since VSCode already integrates with `golangci-lint` via the [Go][vscode-go] extension, you can use the custom `golangci-lint-kube-api-linter` binary as a linter in VSCode.

If your project authors are already using VSCode and have the configuration to lint their code when saving, this can be a seamless integration.

Ensure that your project setup includes building the `golangci-lint-kube-api-linter` binary, then configure the `go.lintTool` and `go.alternateTools` settings in your project `.vscode/settings.json` file:

[vscode-go]: https://code.visualstudio.com/docs/languages/go

```json
{
    "go.lintTool": "golangci-lint",
    "go.alternateTools": {
        "golangci-lint": "${workspaceFolder}/bin/golangci-lint-kube-api-linter"
    }
}
```

Alternatively, you can also replace the binary with a script that runs the `golangci-lint-kube-api-linter` binary, allowing for customization or automatic compilation of the project should it not already exist:

```json
{
    "go.lintTool": "golangci-lint",
    "go.alternateTools": {
        "golangci-lint": "${workspaceFolder}/hack/golangci-lint.sh",
    }
}
```

# Contributing

New linters can be added by following the [New Linter][new-linter] guide.

[new-linter]: docs/new-linter.md

# Linters

For a complete list of available linters and their configuration options, see [docs/linters.md](docs/linters.md).

# License

KAL is licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE) for the full license text.
