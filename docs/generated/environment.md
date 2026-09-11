# Environment Variables

## Environment

Environment is the process bootstrap contract. UI values are optional for
the API binary and are required by ValidateUI when starting the BFF.

 - `MASTARR_DATA_DIR` (default: `/data`) - Persistent database, key, descriptor and journal directory.
 - `MASTARR_CONFIG_FILE` - Optional startup-only YAML configuration file. Empty selects API-managed configuration.
 - `MASTARR_LISTEN_ADDR` (default: `:8080`) - API listener address.
 - `MASTARR_CREDENTIAL_KEY` - Optional base64-encoded 32-byte credential encryption key. Never log this value.
 - `MASTARR_CREDENTIAL_KEY_FILE` - Optional file containing the same base64-encoded key. Mutually exclusive with CredentialKey.
 - `MASTARR_LOG_LEVEL` (default: `info`) - Structured log level: info, debug, warn or error.
 - `MASTARR_UI_API_URL` - API URL used by the BFF. Required when ValidateUI is called.
 - `MASTARR_UI_LISTEN_ADDR` (default: `:8081`) - BFF listener address.
 - `MASTARR_UI_PUBLIC_ORIGIN` - Absolute public origin used for BFF metadata and origin checks. Required by ValidateUI.
