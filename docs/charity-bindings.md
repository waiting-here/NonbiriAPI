# Charity binding workflows

Authorized managers can see existing connections while choosing donated keys.
From a key's management page, collect several exact upstream-model and charity-model
pairs and submit them together. Selections remain available while changing target
models or pages. A connection already present on the selected charity model is
marked and cannot be selected again.

Administrators and level-6 stewards also have a separate **Channel presets** view.
Choose a mainstream channel and save up to 16 exact upstream-model-to-charity-model
pairs. From an approved donation for that channel, apply the preset to all its keys
in one operation. A missing upstream model skips only that pair for that key.
Existing bindings keep their order; applying or editing a preset never enables a
key, approves a donation, refreshes discovery or changes saved model pricing.

## Session API

Use the administrator prefix `/admin/api` or steward prefix `/api/steward`.
These are authenticated browser-session routes, with the existing CSRF,
`Idempotency-Key`, current-role and revision requirements. They are not CallerKey
automation routes. Level-5 stewards retain their existing per-model scope for
manual bindings; preset reads and writes require an administrator or level 6.

IDs, revisions and counts below are canonical decimal strings, except opaque
channel IDs. Pagination uses the existing numbered-page envelope.

| Method and suffix | Request or response |
| --- | --- |
| `GET /donations/{id}/keys/{keyId}/binding-options` | `q,page,page_size`; `{data:[{charity_model_id,full_name,enabled,binding_revision}],pagination,next_cursor:null}` |
| `POST /donations/{id}/keys/{keyId}/bindings/batch` | `{expected_donation_revision,targets:[{charity_model_id,expected_binding_revision,upstream_model_id}]}`; at most 256 distinct pairs within a 256 KiB request |
| `GET /charity-binding-presets` | `q,page,page_size,state=active\|retired\|all`; channel page with `channel_id,name,connector_type,base_url,enabled,state,channel_revision,preset_revision,mapping_count` |
| `GET /charity-binding-presets/{channelId}` | `{channel_id,channel_revision,preset_revision,mappings:[{upstream_model_id,charity_model_id,full_name,binding_revision}]}` |
| `PUT /charity-binding-presets/{channelId}` | `{expected_channel_revision,expected_preset_revision,mappings:[{upstream_model_id,charity_model_id}]}`; at most 16 distinct pairs |
| `POST /donations/{id}/bindings/apply-preset` | `{channel_id,expected_channel_revision,expected_preset_revision,expected_donation_revision,models:[{charity_model_id,expected_binding_revision}]}` |

An unconfigured preset has revision `"0"`; its first save produces `"1"`.
Saving `mappings:[]` clears it and advances the revision. Preset detail includes
current target names and binding revisions; both are null when a target no longer
exists. Deleted targets remain identifiable in the preset so they can be repaired.
Preset changes affect future applications only.

Apply requires a live approved donation, an active enabled channel and a nonempty
preset. The server verifies the donation's channel identity and each key's source
snapshot. The `models` revision set must exactly cover the preset's existing
distinct target models. It is not a way to select only some preset entries.
A target deleted after preview may still carry its previous revision; that
mapping is skipped as `invalid_resource`.

The model-centric candidate endpoint accepts `include_bound=true` to include
already-bound candidates. It supplies `current_binding_id` (nullable),
`other_binding_count` and up to three `other_bindings` entries containing
`charity_model_id,full_name,binding_id`. Those summaries are filtered by the
manager's current permissions. Existing queries retain their default behavior.

## Batch results and retries

Both new batch operations return HTTP 200 with a compact receipt:

```json
{
  "items": [
    {"donation_key_id": "12", "results": ["added", "missing_model"]},
    {"donation_key_id": "13", "results": ["already_bound", "added"]}
  ],
  "models": [
    {"charity_model_id": "7", "binding_revision": "4"},
    {"charity_model_id": "8", "binding_revision": "2"}
  ],
  "added_count": "2"
}
```

Each `results` entry corresponds to the same position in the submitted `targets`,
or in the preset's ordered `mappings` snapshot. Keep that snapshot with the
request so a later preset edit cannot change the meaning of a saved receipt.
Manual-key batches contain one key row; preset application can contain up to 100.

| Result | Meaning |
| --- | --- |
| `added` | The connection was created. |
| `already_bound` | The exact connection already exists and was preserved. |
| `missing_model` | This key has no matching discovered or manual upstream model. |
| `invalid_resource` | The individual key or target can no longer be used. |
| `capacity_exceeded` | The target model has reached its binding limit. |

Individual skipped items do not block other pairs. A database failure, revoked
authority or a terminal whole donation fails the operation. Stale revisions return
409 and roll back the operation. Level-5 authority is checked for every requested
target; an unauthorized target rejects the whole request without revealing whether
that model exists.

After an uncertain result, retry the identical route, body and idempotency key.
After a confirmed receipt, refresh the displayed models and bindings; a failed
refresh does not justify resubmitting with a new key. Existing per-model batch
routes retain their original all-or-nothing duplicate and capacity behavior.

## Storage

Channel presets are shared configuration, retained with the channel and excluded
from personal account exports. They contain no donor or operator identity.
Mutations use the existing role-scoped control receipts and their retention
boundary. Account export format remains 23. Schema 26 adds the preset structures;
use the matching [offline converter](database-upgrades.md) for an existing database.
