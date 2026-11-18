BEGIN;

CREATE TABLE projects (
    id UUID PRIMARY KEY,
    repo_url TEXT NOT NULL,
    observed_sha TEXT,
    observed_revision BIGINT NOT NULL DEFAULT 0,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE source_events (
    id UUID PRIMARY KEY,
    project_id UUID NOT NULL REFERENCES projects (id),
    previous_sha TEXT,
    sha TEXT NOT NULL,
    observed_revision BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (project_id, observed_revision)
);

COMMIT;
