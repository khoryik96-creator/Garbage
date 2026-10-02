# Kano reference and JobAdder field mappings

This prototype uses [Kano](https://github.com/khoryik96-creator/Kano) as a source
reference at commit [`58c857dad907cf73c0e3281ba449e142ce81b9c9`](https://github.com/khoryik96-creator/Kano/tree/58c857dad907cf73c0e3281ba449e142ce81b9c9).
The reference supplies field names and integration lessons. It does not establish
the public API's write contract or enable a live connection in Garbage Truck.

## Two distinct API contracts

`modules/kano_jobadder_api.js` performs candidate lookup with the public OAuth API:

- Authorization and token endpoints use `https://id.jobadder.com/connect/authorize`
  and `https://id.jobadder.com/connect/token`.
- Lookup uses `/v2/candidates`, with scopes `read read_candidate offline_access`.
  It reads `items`, `totalCount`, and `links.next` for pagination.
- Prefer the regional API base returned by OAuth. The generic base is
  `https://api.jobadder.com/v2`; Kano also supports regional JobAdder hosts.
  Validate HTTPS, host, and pagination origin before sending authorization headers.
- Employer data in the public lookup uses `employment.current` and
  `employment.history`, unlike the browser record's `currentEmployer`.

Kano's field-writing modules instead use the authenticated browser route
`/spa/api/candidates/{id}`, session cookies, and `x-jobadder-spa: 1`. They can send a
whole candidate record with `PUT`. Browser payloads must not be sent to the public
OAuth API. Garbage Truck's future connector will verify public read and write
schemas, permissions, pagination, picklists, and concurrency separately.

No browser tokens, session cookies, credential storage, or candidate records were
copied from Kano. Its source checkout is a reference outside the project checkout.

## Versioned catalogue

[field_catalog.json](../internal/domain/field_catalog.json) records
20 observed fields, their source files, notes, and account-specific status. The
read-only `/api/fields` endpoint exposes this metadata, including the source commit.
`public_v2_mapping_verified` is false for every field. `enabled_for_runs` is true
only for Country, whose current implementation still uses synthetic profiles.

| Field group | Browser-record shape observed in Kano |
|---|---|
| Country | `address.country` and `address.countryCode` together |
| Name and contact | `firstName`, `lastName`, `email`, separate `mobile` and `phone` objects |
| LinkedIn | `socialLinks.linkedInUrl` |
| Current role and employer | `currentPosition`, `currentEmployer` |
| Salary | `currentSalary`, `idealSalary`; composite amount/range/rate blocks |
| Notice / availability | `availability.type`, `availability.date`; legacy alternatives also observed |
| Recruitment source, state, work type | `source`, `address.state`, `workType` |

Kano observes these custom-field IDs in its account:

| ID | Meaning observed | Value shape |
|---|---|---|
| 1 | Industry | `valueList` and `value`, multi-select |
| 2 | Industry sub-category | `valueList` and `value`, independent multi-select |
| 3 | IT skills | `valueList` and `value`, multi-select |
| 4 | Currency | `value`, full picklist label |
| 5 | Residential status | `value`, account-defined picklist |
| 7 | Professional qualifications | `valueList` and `value`, multi-select |

Catalogue paths such as `customFields.1.valueList` identify **field ID 1**, not an
array index or an executable JSON pointer. IDs, options, and selection semantics
must be discovered for the connected account. Residential status must not be
assumed to mean nationality, work rights, visa status, or willingness to relocate.

## Missing-field and preservation rules

`internal/connectors/jobadder_fields.go` provides a read-only Country observation helper for
the browser-record shape. A populated name or code means existing Country, even
when the other component is blank or omitted. Conflicting or unrecognized values
also remain existing data. Only two explicitly empty components mean missing;
partial or malformed records report unavailable and block a missing-field fill.
This helper is ready for adapter tests; it is not a live connector.

Garbage Truck preserves `Unknown`, `N/A`, `TBC`, and `-`. Kano's CV-fill placeholder
handling and selected-field overwrite behavior are not adopted. Mobile and phone
remain separate targets. Country cannot be inferred from a calling code, and
state/suburb shortcuts must not modify other address fields during a Country run.

Salary, availability, custom fields, and work type need preservation checks. Kano
explicitly protects these during whole-record updates; that is evidence of a
write-contract risk, not proof that a public partial update is safe. Zero salary is
a value. Currency uses discovered labels, and long notice periods must not be
silently capped. Future extraction also needs dated evidence for current employment
and salary. Resume selection must verify attachment type and candidate ownership;
the first attachment could be a different document.

The main source files are `modules/kano_cv_fields.js`, `modules/kano_cv_fill.js`,
`modules/kano_panel_write.js`, `modules/kano_salary_api.js`,
`modules/kano_salary_write.js`, and `modules/kano_jobadder_api.js`. The integration
sequence remains [the architecture plan](architecture.md): public read-only access,
verified mappings, then tested write and concurrency guarantees.
