# vsp-extensions

Extensions for [vsp](https://github.com/oisee/vibing-steampunk), the ABAP
MCP server and CLI, built on its extension API (`pkg/mcpext`). They cover
SAP work that has no place in vsp's core — objects that ADT does not reach
and that need a background job or an add-on of their own.

`vspx` is vsp with these extensions built in: every flag, command, setting
and safety switch of vsp works unchanged, and `SAP()` gains the actions below.

| Extension | `SAP()` actions | What it does |
|---|---|---|
| `forms` | `read`/`edit` `SSFO`, `FORM`, `SFPF`, `SFPI`; `create SSFO`/`FORM`; `vspx form` | Print forms as documents: Smart Forms, SAPscript forms, Adobe forms and interfaces. Needs the ABAP form service in [`abap/`](abap/README.md) |
| `classic-badi` | `create`/`delete SXCI` | Classic BAdI implementations (SE19), with their class and filters |
| `idoc-types` | `read`/`create`/`edit`/`delete SEGM`, `… IEXT` | IDoc segment types (WE31) and extensions of basic types (WE30), including release handling |
| `transport-import` | `import TRANSPORT`, `vspx transport import` | Imports released requests into the connected system, as STMS_IMPORT does there; synchronous or as a background task |
| `cts-plus` | `read`/`edit`/`release`/`import CTSREQUEST`, `read CTSQUEUE` | Non-ABAP requests (CTS+, e.g. SAP PI) on the domain controller: CTS project, release, import queue, import into a system of the domain |

`SAP(action="help")` lists them with examples; `vspx --version` names the
extensions and their versions.

## How classic-badi and idoc-types work

Neither SE19 nor WE31/WE30 has an ADT editor or a remote-enabled API. The
extensions write the steps those transactions take into a temporary report in
`$TMP`, run it as a background job over RFC (XBP) and read the outcome from the
job log. The report is deleted afterwards; one left behind by an interrupted
run is swept by the next. SAP records the objects in the transport request
itself, as the transactions do.

They therefore need vsp's RFC connection (`rfc_host`/`rfc_sysnr` in
`.vsp.json`) and authorization to schedule background jobs.

Every write goes through vsp's own gates: `--read-only` refuses it before
the handler runs, `--disallowed-ops`, `--allowed-packages` and the
transportable-edit policy apply as for a built-in write.

## Transport import

The import calls `CTS_API_IMPORT_CHANGE_REQUEST` in the connected system
itself, so it always imports there and nowhere else. It is off unless the
system's entry in `.vsp.json` allows it:

```json
{"systems": {"qas": {"enable_transports": true, "transport_read_only": true,
  "allowed_transports": ["DEVK*"],
  "extensions": {"transport-import": {"allow": true}}}}}
```

with the MCP server started as `vspx -s qas --disallowed-ops CDUAWLT`.

It is a transport operation: it needs `enable_transports`, and
`--read-only`, `--disallowed-ops X` and `allowed_transports` apply.
`transport_read_only` guards the requests themselves (create, change,
release) and does not refuse an import. A quality or production system that
should take imports and nothing else runs as above instead of `--read-only`:
`--disallowed-ops CDUAWLT` refuses every other change and every execution.

## CTS+ (non-ABAP requests)

A request of a non-ABAP system -- `D22K…` for an SAP PI system `D22` --
lives in the Transport Organizer of the domain controller, but belongs to its
source system, and the classic `TR_*` function modules refuse to change it
there. `cts-plus` runs on a server connected to the domain controller and
uses the `CTS_WBO_*` and `SCTS_API_*` function modules over classic RFC, the
path the CTS Organizer Web UI and STMS take:

```text
SAP(action="read",    target="CTSREQUEST D22K900240")
SAP(action="release", target="CTSREQUEST D22K900240", params={"project": "DEV_P00005"})
SAP(action="read",    target="CTSQUEUE QAS", params={"request": "D22K900240"})
SAP(action="import",  target="CTSREQUEST D22K900240", params={"system": "QAS"})
```

A PI export (ESR or Integration Directory) leaves the request without a CTS
project when the PI client has no way to name one; when the domain makes the
project mandatory, CTS refuses the release. `release` with `project` assigns
it first (`edit` does only that), and reads the request back after each step.

Assigning a project and releasing change the request: they need
`enable_transports` and a request in `allowed_transports`, and
`transport_read_only` refuses them. A request of the controller itself is
left to the built-in transport actions. Importing is off unless the system
lists the target systems:

```json
{"systems": {"dev": {"enable_transports": true, "allowed_transports": ["DEVK*", "D22K*"],
  "extensions": {"cts-plus": {"import_systems": ["Q22"]}}}}}
```

## Build

```bash
go build -o vspx ./cmd/vspx
```

Go 1.26 or later. Release binaries are on the
[releases page](https://github.com/gorynychzmey/vsp-extensions/releases).

The extension API is proposed upstream
([oisee/vibing-steampunk#291](https://github.com/oisee/vibing-steampunk/issues/291));
until it is merged, `go.mod` takes vsp from the branch that carries it
(`gorynychzmey/vibing-steampunk`, `feat/mcpext-env`). To build against a
local vsp checkout, put a `go.work` next to `go.mod`:

```
go 1.26.0

use .

replace github.com/oisee/vibing-steampunk => ../vibing-steampunk
```

CI builds every push and pull request, and every night against vsp's newest
core.

## License

MIT
