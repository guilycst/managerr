# UI component compatibility and reuse ledger

Task U-00, rechecked 2026-09-11. This record freezes the public UI module
baseline before dashboard templates are added. Examples use synthetic
`Example Film`, `Example Series`, and `https://example.test` values.

## Selected module baseline

| Module | Version | Identity evidence | Role |
| --- | --- | --- | --- |
| `github.com/araihu/goshtoso` | `v0.3.0` | Go proxy metadata: origin `2c4e86ab838868335e99f485bbab8caa88b54c8b`, module sum `h1:6+fTBCzDHFCfEtw8UGH3IePfrDsCyDvaZPIEn3V85i8=` | Components, embedded CSS, icons, runtime manifest and metadata |
| `github.com/araihu/goshtoso-app-shells` | `v0.1.9-0.20260910224508-5b2222e54637` | Go proxy metadata: commit `5b2222e54637f895897fa6dfd494b8c24b2097d9`, module sum `h1:Ur4NL5uoB0gIRtL72hXMKHwop/K76m1fkPzYTEeqNjw=` | `consoleshell` operations frame and shell assets |
| `github.com/a-h/templ` | `v0.3.1020` | Go module metadata, module sum `h1:ypAT/L5ySWEnZ6Zft/5yfoWXYYkhFNvEFOeeqecg4tw=` | Consumer-owned `.templ` source and generated templates |

`ui/go.mod` also retains the existing `github.com/oapi-codegen/runtime v1.7.0`
pin for the generated HTTP client. No `replace` directive or `go.work` is
needed. The shell pseudo-version declares an older Goshtoso pseudo-version in
its own `go.mod`; the UI's direct `v0.3.0` requirement wins under Go minimal
version selection, and the selected public packages compile together.

The Goshtoso release endpoint returned [v0.3.0](https://github.com/araihu/goshtoso/releases/tag/v0.3.0),
published at `2026-09-11T02:04:47Z`. The App Shells release endpoint returned
[v0.1.4](https://github.com/araihu/goshtoso-app-shells/releases/tag/v0.1.4),
while the module tag list also contains `v0.1.8`. The `v0.1.8` shell uses the
HTMX 2 event names `htmx:beforeSwap`, `htmx:afterSettle`, and
`htmx:historyRestore`, and requires Goshtoso `v0.2.0`. It is incompatible with
the HTMX 4 lifecycle used by Goshtoso `v0.3.0`.

The selected public App Shells commit is therefore the untagged
`5b2222e54637f895897fa6dfd494b8c24b2097d9` pseudo-version. Its shell uses the
HTMX 4 names `htmx:before:swap` and `htmx:after:settle`, was generated with
templ `v0.3.1020`, and exposes `consoleshell.Page.Metadata` for the complete
Goshtoso head metadata contract. This is a compatibility baseline, not a
claim that App Shells has published a matching stable release. Browser and
container verification remain release gates.

The Goshtoso `v0.3.0` tag is annotated. Its tag object is
`6b6374f6f9112e98a3ccbdf9d7fbc7121f591a1e`, which peels to commit
`2c4e86ab838868335e99f485bbab8caa88b54c8b`. That peeled commit matches the Go
proxy `Origin.Hash`; there is no tag/proxy mismatch. The committed `go.sum`
still pins the module bytes used by the build.

## Runtime and shell contract

Use `consoleshell` because Mastarr is an operations control plane. The BFF
will configure:

- `consoleshell.Layout` for complete documents and `consoleshell.Fragment` for
  HTMX responses;
- `consoleshell.Config` with `Brand`, stable sidebar `Navigation`,
  `AppearanceConfig{DefaultTheme: "goshtoso", InitialColorScheme: system}`,
  `InteractionConfig{EnableHTMX: true, LocalRuntime: true,
  NavigationOOB: true}`;
- `consoleshell.Head` only when a custom outer layout needs the shell runtime;
  ordinary pages should use `Layout`;
- `head.Metadata` through `consoleshell.Page.Metadata`, with a route-specific
  title, description, absolute HTTPS canonical, generic preview image, MIME
  type, dimensions and alt text;
- `assets.Handler()` at `/assets/` and
  `consoleshell/assets.Handler()` at `/consoleshell/assets/` on the same
  `net/http` mux. Neither handler is wrapped in `http.StripPrefix`.

`LocalRuntime: true` makes the v0.0.1 container self-host its exact Goshtoso
CSS and JavaScript. `head.Dependencies(head.WithLocalRuntime())` emits direct,
version-matched local script tags for the ordered HTMX 4, Alpine 3 and Goshtoso
runtime assets. It does not emit a dependency loader or a fallback CDN path.
The selected Goshtoso runtime metadata reports HTMX `4.0.0`, Alpine.js `3.17.2`,
Alpine collapse/focus/mask `3.17.2`, and the matching `hx-alpine-compat`
extension. The BFF must not add a second runtime or manually initialize swapped
trees.

`themes.BuiltIn()` in Goshtoso `v0.3.0` includes both `goshtoso` and `minimal`.
The browser task still must exercise both themes, plus light, dark and system
color modes, at 390 px and 1440 px. This ledger does not claim those browser
checks passed.

## Surface decisions

| Product need | Public surface | Decision | Consumer boundary |
| --- | --- | --- | --- |
| Persistent operations frame, responsive navigation and one main scroll region | `github.com/araihu/goshtoso-app-shells/consoleshell`: `Layout`, `Fragment`, `Head`, `Config`, `Page`; `consoleshell/assets`: `Handler`, `StylesheetURL`, `ScriptURL` | **reuse** | Shell owns frame, drawer, first-paint mode and fragment lifecycle. Mastarr owns routes, navigation labels, selection and API state. |
| Head, social metadata and local runtime loading | `github.com/araihu/goshtoso/components/head`: `Metadata`, `Dependencies`, `WithLocalRuntime`; `github.com/araihu/goshtoso/assets`: `Handler`, `DefaultRuntimeManifest` | **reuse** | Route metadata and the generic public-safe preview asset remain BFF inputs. No inventory identifiers enter metadata. |
| Discovery, media and trash lists | `pageheader.PageHeader`, `toolbar.Toolbar`, `panel.Panel`, `table.Table`, `pagination.Pagination`, `skeleton.Skeleton`, `emptystate.EmptyState`, `badge.Badge` | **compose** | API pagination/filter state, columns, status vocabulary and destructive scope remain application decisions. |
| Media or discovery detail | `breadcrumbs.Breadcrumbs`, `pageheader.PageHeader`, `panel.Panel`, `tabs.Tabs`, `badge.Badge`, `alert.Alert` | **compose** | Provider IDs, source evidence, subtitle associations, seeding impact and unknown coverage are app-owned semantic content. |
| Registration/import and file-operation workflow | `steps.Steps`, `form.Form`, `form.FieldGroup`, `form.FormErrors`, `alert.Alert`, `button.Button`, `actiongroup.ActionGroup`, `modal.Dialog` | **compose** | The API plan revision, two approvals, exact manifest, stale checks, retries and cancellation remain authoritative. A button state never grants permission. |
| Correcting title, season, episode and subtitle identity | `combobox.Combobox`, `selectfield.Select` (package path `components/select`), `checkbox.Checkbox`, `radio.Radio`, `textinput.TextInput`, `textarea.Textarea` | **compose** | Search and selected values come from API responses and URL state. Disable component persistence when the route/API owns selection; never let browser state override a plan revision. |
| Settings and credential replacement forms | `form`, `textinput`, `textarea`, `select`, `checkbox`, `alert`, `button` | **compose** | Source labels, restart requirement, ETag/precondition failures and redaction are app-owned. Secrets never enter browser storage or rendered error text. |
| Status and action icons | `icon.Icon` with `icon/heroicons` typed symbols and relative sprite URL | **reuse** | Every labelled icon has an accessible `Label`; decorative icons set `Decorative: true`. No pasted SVG. |
| Product-specific identity, exact-file evidence and workflow effect ledger | No single Goshtoso component owns this domain model | **gap** | Add small semantic markup inside `panel`, `table`, `steps` and `alert` slots. Add app CSS or JavaScript only for a named behavior that supported slots cannot express, with a focused test. |
| Charts, Markdown, PDF and slide rendering | Goshtoso Charts and Margo are public options but no v0.0.1 route needs them | **skip** | Counts and status tables meet the dashboard scope. Add a module only when a real route requires chart/document rendering. |

## Application pattern map

The first BFF implementation should follow the public patterns documented by
the selected ecosystem:

1. App Shell: `consoleshell` frame with sidebar navigation for `/discoveries`,
   `/media`, `/workflows`, `/actions`, `/trash` and `/settings`.
2. Operations List: `PageHeader` + `Toolbar` + result count live region +
   `Table` + loading/empty/error state for discovery, media and trash lists.
3. Detail Workspace: breadcrumbs + page header + identity/status panels +
   route-local tabs for discovery, media, action and trash details.
4. Multi-step Workflow: page header + `Steps` + exact plan panels + server
   validation + review alert + explicit submit/cancel controls for registration,
   import, placement, trash, restore and purge.

Keep normal links as the recovery path. HTMX may enhance feature-region
navigation and form responses; fragments return rendered HTML, preserve URL
selection and focus, and retain the shell's one-main-region contract.

## Evidence and limits

The following checks passed against this baseline:

- `GOWORK=off go list -m -versions` found Goshtoso `v0.3.0`, App Shells
  `v0.1.8`, and templ `v0.3.1020` in the public module index.
- `GOWORK=off go list -m -json` resolved all three selected versions and their
  origin identities above.
- A synthetic external consumer compiled and rendered both
  `consoleshell.Layout` and `consoleshell.Fragment` with Goshtoso `v0.3.0`, the
  selected App Shells pseudo-version, templ `v0.3.1020`, local runtime and
  `https://example.test/preview.png` metadata.
- A UI-module `go build` passed for the selected shell, asset, head and planned
  component packages. `GOWORK=off go mod verify` passed.

The UI module has no consumer Go package until U-01, so
`GOWORK=off go test ./...` and `go vet ./...` report `no packages to test` and
`no packages to vet`. A direct `go test` of upstream component packages also
compiles their test files and requires their test-only `testify` and
Playwright dependencies; it is not the consumer check and is intentionally not
added to this empty UI module. U-01 owns templ generation, BFF code and the
first consumer package; U-05 owns browser evidence.
