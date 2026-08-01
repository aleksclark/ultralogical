-- +goose Up

-- What a provider registration can actually do, discovered by probing its
-- control plane at registration rather than inferred from its kind. A cluster
-- that cannot serve an environment health endpoint must be able to say so, so
-- a flow depending on that policy is refused instead of hanging on a gate that
-- can never open.
ALTER TABLE provider_instances
  ADD COLUMN capabilities jsonb NOT NULL DEFAULT '{}'::jsonb;

-- +goose Down
ALTER TABLE provider_instances DROP COLUMN capabilities;
