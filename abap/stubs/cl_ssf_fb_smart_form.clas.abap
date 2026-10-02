"! Stub of SAP's Smart Form object for abaplint: only what the form service
"! uses. Not installed anywhere; abaplint/deps has no copy of the class.
CLASS cl_ssf_fb_smart_form DEFINITION PUBLIC CREATE PUBLIC.
  PUBLIC SECTION.
    CONSTANTS c_mode_edit TYPE c LENGTH 1 VALUE 'E'.
    DATA header TYPE ssfhead.
    DATA korrnum TYPE trkorr.
    DATA enqueued TYPE c LENGTH 1.
    METHODS xml_upload
      IMPORTING dom      TYPE REF TO if_ixml_element
                formname TYPE tdsfname
                language TYPE sylangu
      CHANGING  sform    TYPE REF TO cl_ssf_fb_smart_form
      RAISING   cx_ssf_fb.
    METHODS store
      IMPORTING im_active   TYPE abap_bool OPTIONAL
                im_formname TYPE tdsfname
                im_language TYPE sylangu
      RAISING   cx_ssf_fb.
    METHODS dequeue
      IMPORTING formname TYPE tdsfname.
    METHODS load
      IMPORTING im_formname    TYPE tdsfname
                im_active      TYPE abap_bool OPTIONAL
                im_language    TYPE sylangu OPTIONAL
      EXPORTING ex_fmnumb      TYPE string
                ex_fmnumb_test TYPE string
      RAISING   cx_ssf_fb.
    METHODS xml_download
      IMPORTING parent   TYPE REF TO if_ixml_element
      CHANGING  document TYPE REF TO if_ixml_document.
  PROTECTED SECTION.
    METHODS set_correction_request.
ENDCLASS.

CLASS cl_ssf_fb_smart_form IMPLEMENTATION.
  METHOD xml_upload.
  ENDMETHOD.
  METHOD store.
  ENDMETHOD.
  METHOD dequeue.
  ENDMETHOD.
  METHOD load.
  ENDMETHOD.
  METHOD xml_download.
  ENDMETHOD.
  METHOD set_correction_request.
  ENDMETHOD.
ENDCLASS.
