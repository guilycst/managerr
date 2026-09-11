# OpenAPI fragments

The YAML files in this directory are the editable API source. The bundler reads
them in lexical order and writes `api/openapi.yaml`, which is generated output.
Run `./scripts/generate.sh --write` after changing a fragment. Never edit the
bundle directly.
