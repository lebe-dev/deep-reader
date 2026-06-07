-- +goose Up

-- force_json_object: when set, the profile skips the json_schema response format
-- entirely and asks the provider for json_object directly on every LLM call.
-- This is the per-profile equivalent of the automatic json_schema→json_object
-- fallback (see internal/llm: isSchemaUnsupported), made sticky so a provider
-- that always rejects json_schema (e.g. an OpenRouter model whose data-policy /
-- guardrail leaves zero structured-outputs endpoints) need not be probed and
-- fail once on every single call. Default 0 preserves the existing behaviour
-- (prefer json_schema, fall back on rejection).
ALTER TABLE llm_providers ADD COLUMN force_json_object INTEGER NOT NULL DEFAULT 0;

-- +goose Down

ALTER TABLE llm_providers DROP COLUMN force_json_object;
