# Environment Variables

## Environment

Environment is the process bootstrap contract. UI values are optional for
the API binary and are required by ValidateUI when starting the BFF.

 - `MANAGERR_DATA_DIR` (default: `/data`) - Persistent database, key, descriptor and journal directory.
 - `MANAGERR_CONFIG_FILE` - Optional startup-only YAML configuration file. Empty selects API-managed configuration.
 - `MANAGERR_LISTEN_ADDR` (default: `:8080`) - API listener address.
 - `MANAGERR_CREDENTIAL_KEY` - Optional base64-encoded 32-byte credential encryption key. Never log this value.
 - `MANAGERR_CREDENTIAL_KEY_FILE` - Optional file containing the same base64-encoded key. Mutually exclusive with CredentialKey.
 - `MANAGERR_LOG_LEVEL` (default: `info`) - Structured log level: info, debug, warn or error.
 - `MANAGERR_UI_API_URL` - API URL used by the BFF. Required when ValidateUI is called.
 - `MANAGERR_UI_LISTEN_ADDR` (default: `:8081`) - BFF listener address.
 - `MANAGERR_UI_PUBLIC_ORIGIN` - Absolute public origin used for BFF metadata and origin checks. Required by ValidateUI.

