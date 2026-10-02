# Form service (ABAP)

The `forms` extension talks to `ZCL_VSP_FORM_SERVICE`, a service of
ZADT_VSP's WebSocket handler (domain `form`). It reads and writes Smart Forms
(the SMARTFORMS download XML), SAPscript forms (abapGit's FORM structure) and
Adobe forms and interfaces (the SFP XML, layouts as XDP), through the APIs
the form editors use.

| Class | What it is |
|---|---|
| `ZCL_VSP_FORM_SERVICE` | the service: `info`, `read`, `write` |
| `ZCL_VSP_SSF_SILENT` | Smart Form upload without the editor's dialogs |
| `ZCX_VSP_FORM` | its exception |

## Install

1. ZADT_VSP must be installed: the service implements `ZIF_VSP_SERVICE` and
   uses `ZCL_VSP_UTILS`.
2. Pull this folder with abapGit into a package of your own (for example
   `ZBC_VSP_FORMS`), not into ZADT_VSP's: the two are updated independently.
3. Register the service in `ZCL_VSP_APC_HANDLER`'s constructor:

   ```abap
   APPEND NEW zcl_vsp_form_service( ) TO gt_services.
   ```

   This step goes away once ZADT_VSP finds its services by itself.

A system without the service answers the extension with `UNKNOWN_DOMAIN`,
and the extension says what to install.

## Why activation happens elsewhere

The service runs in the APC session, where the workbench activation cannot
run (it waits for asynchronous tasks). Adobe forms and interfaces therefore
come back inactive, and the extension activates them over ADT. A replaced
Adobe form that does not activate is written back as it was.
