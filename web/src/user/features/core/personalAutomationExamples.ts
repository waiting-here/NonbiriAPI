export const personalAutomationExamples = {
  read: 'USER_ORIGIN=\'https://your-instance.example\'\nCALLER_KEY=\'YOUR_CALLER_KEY\'\ncurl --fail-with-body "$USER_ORIGIN/api/automation/endpoints?q=Personal&page=1&page_size=20" -H "Authorization: Bearer $CALLER_KEY"\ncurl --fail-with-body "$USER_ORIGIN/api/automation/endpoints/42/keys?page=1&page_size=20" -H "Authorization: Bearer $CALLER_KEY"\ncurl --fail-with-body "$USER_ORIGIN/api/automation/models?page=1&page_size=20" -H "Authorization: Bearer $CALLER_KEY"\ncurl --fail-with-body "$USER_ORIGIN/api/automation/models/23/bindings?page=1&page_size=20" -H "Authorization: Bearer $CALLER_KEY"',
  importBody:
    '{\n  "ownership_confirmed": true,\n  "keys": [\n    {\n      "secret": "fictional-provider-secret",\n      "note": "Personal key",\n      "enabled": true,\n      "max_concurrency": 0,\n      "max_rpm": 0\n    }\n  ]\n}',
  importRequest:
    'IMPORT_OPERATION_KEY=\'personal-import-example-00000001\'\ncurl --fail-with-body "$USER_ORIGIN/api/automation/endpoints/42/keys/batch-import" -H "Authorization: Bearer $CALLER_KEY" -H \'Content-Type: application/json\' -H "Idempotency-Key: $IMPORT_OPERATION_KEY" --data-binary @import.json',
  bindBody:
    '{\n  "endpoint_key_ids": [\n    "81",\n    "82"\n  ],\n  "upstream_model_id": "example/model",\n  "catalog_mode": "manual"\n}',
  bindRequest:
    'BIND_OPERATION_KEY=\'personal-binding-example-00000001\'\ncurl --fail-with-body "$USER_ORIGIN/api/automation/models/23/bindings/batch" -H "Authorization: Bearer $CALLER_KEY" -H \'Content-Type: application/json\' -H "Idempotency-Key: $BIND_OPERATION_KEY" --data-binary @bindings.json',
  resultExample:
    '{\n  "model_id": "23",\n  "results": [\n    {\n      "index": 0,\n      "endpoint_key_id": "81",\n      "status": "success",\n      "outcome": "existing",\n      "binding_id": "91"\n    },\n    {\n      "index": 1,\n      "endpoint_key_id": "82",\n      "status": "incomplete",\n      "code": "incomplete",\n      "message": "No result is confirmed; use the same idempotency key to check and continue."\n    }\n  ]\n}',
} as const;
