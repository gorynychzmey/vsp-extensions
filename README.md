# vsp-extensions

Extensions for [vsp](https://github.com/oisee/vibing-steampunk), the ABAP
MCP server and CLI, built on its extension API (`pkg/mcpext`). They cover
SAP work that has no place in vsp's core — objects that ADT does not reach
and that need a background job or an add-on of their own.

`vspx` is vsp with these extensions built in: every flag, command, setting
and safety switch of vsp works unchanged, and `SAP()` gains the actions below.

| Extension | `SAP()` actions | What it does |
|---|---|---|
| `classic-badi` | `create`/`delete SXCI` | Classic BAdI implementations (SE19), with their class and filters |
| `idoc-types` | `read`/`create`/`edit`/`delete SEGM`, `… IEXT` | IDoc segment types (WE31) and extensions of basic types (WE30), including release handling |

`SAP(action="help")` lists them with examples; `vspx --version` names the
extensions and their versions.

## How they work

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

## Build

```bash
go build -o vspx ./cmd/vspx
```

Go 1.26 or later.

## License

MIT
