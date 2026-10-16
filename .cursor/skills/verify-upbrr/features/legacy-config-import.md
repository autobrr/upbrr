# Legacy configuration import

## Sub-features

Convert legacy settings and tracker selections into current configuration, including supported aliases.

## How to get to it (user POV)

Use the application's legacy configuration import flow with an existing legacy config file.

## Driving it with Go tests

Run `go test ./internal/config/legacy` to exercise the import entry point and assert converted configuration and warnings.

## Gotchas

Unsupported tracker selections are omitted from defaults/preferred selection and reported as warnings. Tests should use generated or synthetic values, never real credentials.
