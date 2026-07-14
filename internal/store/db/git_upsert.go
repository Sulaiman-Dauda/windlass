package db

import "context"

// UpsertGitConnection creates a connection or refreshes the stored token when
// one with the same name already exists (reconnecting the same account).
func (q *Queries) UpsertGitConnection(ctx context.Context, arg CreateGitConnectionParams) (GitConnection, error) {
	row := q.db.QueryRowContext(ctx, `
		INSERT INTO git_connections (provider, name, token_enc) VALUES (?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET provider = excluded.provider, token_enc = excluded.token_enc
		RETURNING id, provider, name, token_enc, created_at`,
		arg.Provider, arg.Name, arg.TokenEnc)
	var c GitConnection
	err := row.Scan(&c.ID, &c.Provider, &c.Name, &c.TokenEnc, &c.CreatedAt)
	return c, err
}
