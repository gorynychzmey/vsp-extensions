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

## Build

```bash
go build -o vspx ./cmd/vspx
```

Go 1.26 or later.

## License

MIT
