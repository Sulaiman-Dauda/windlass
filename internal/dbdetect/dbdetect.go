// Package dbdetect recognises the database service of a Compose project, for
// backups that dump it and staging copies that load it.
package dbdetect

import (
	"strings"

	"github.com/windlass-dev/windlass/internal/agent"
)

// Best returns the project's database container and its engine.
// The engine is empty when the project has none. A running container beats a
// stopped one, and an exact name match beats a looser one, so a real database
// is chosen over something like postgres-exporter in the same project.
func Best(containers []agent.Container) (agent.Container, string) {
	var best agent.Container
	var bestEngine string
	bestRank := 0
	for _, c := range containers {
		engine, exact := Engine(c.ComposeService, c.Image)
		if engine == "" {
			continue
		}
		rank := 1
		if exact {
			rank++
		}
		if c.State == "running" {
			rank += 2
		}
		if rank > bestRank {
			best, bestEngine, bestRank = c, engine, rank
		}
	}
	return best, bestEngine
}

// OtherVolumes returns the named volumes mounted by database services other
// than chosen, keyed by volume with the service as the value. Only one
// database is dumped and loaded, and a running database's files are not safe
// to copy, so these volumes are not copied at all. Looser matches count too:
// leaving out an exporter's volume costs less than copying a live database.
func OtherVolumes(cfg agent.ResolvedConfig, chosen string) map[string]string {
	out := map[string]string{}
	for name, svc := range cfg.Services {
		if name == chosen {
			continue
		}
		if engine, _ := Engine(name, svc.Image); engine == "" {
			continue
		}
		for _, m := range svc.Mounts {
			if m.Type == "volume" {
				out[m.Source] = name
			}
		}
	}
	return out
}

// Engine recognises a database container by its compose service name
// or image. An exact match is a service or image named after the database,
// ignoring registry, namespace and tag. A looser match is any image whose
// repository path mentions it, such as mysql/community-server or
// bitnami/postgresql-repmgr.
func Engine(service, image string) (engine string, exact bool) {
	repo := strings.ToLower(image)
	repo, _, _ = strings.Cut(repo, "@")
	if i := strings.LastIndex(repo, ":"); i > strings.LastIndex(repo, "/") {
		repo = repo[:i]
	}
	name := repo[strings.LastIndex(repo, "/")+1:]
	for _, n := range []string{strings.ToLower(service), name} {
		switch n {
		case "postgres", "postgresql", "postgis", "pgvector", "timescaledb", "timescaledb-ha":
			return "postgres", true
		case "mysql", "mysql-server", "mariadb", "percona-server":
			return "mysql", true
		}
	}
	switch {
	case strings.Contains(repo, "postgres"):
		return "postgres", false
	case strings.Contains(repo, "mysql") || strings.Contains(repo, "mariadb"):
		return "mysql", false
	}
	return "", false
}

// DumpCommands run inside the database container through sh, so the
// variables expand from the container's environment. They read the official
// images' variables and, for Postgres, Bitnami's POSTGRESQL_* names, so the
// dump is of the application's database rather than an empty default one.
// Postgres falls back to the postgres user and a database named after the
// user, and MariaDB 11 ships mariadb-dump without the mysqldump name. Neither
// may prompt: pg_dump gets -w, and each password is passed only when the
// environment has one, so a database without usable credentials fails at once
// instead of waiting.
var DumpCommands = map[string]string{
	"postgres": `u="${POSTGRES_USER:-${POSTGRESQL_USERNAME:-postgres}}"; ` +
		`db="${POSTGRES_DB:-${POSTGRESQL_DATABASE:-$u}}"; ` +
		`pw="${POSTGRES_PASSWORD:-$POSTGRESQL_PASSWORD}"; ` +
		`[ -n "$pw" ] && export PGPASSWORD="$pw"; ` +
		`exec pg_dump -w -U "$u" "$db"`,
	"mysql": `dump=mysqldump; command -v mariadb-dump >/dev/null 2>&1 && dump=mariadb-dump; ` +
		`pw="${MYSQL_ROOT_PASSWORD:-$MARIADB_ROOT_PASSWORD}"; ` +
		`set -- --all-databases -uroot; [ -n "$pw" ] && set -- "$@" -p"$pw"; ` +
		`exec "$dump" "$@"`,
}

// The staging commands below read the same variables as DumpCommands. They
// run against live's database and then the staging copy's, each with its own
// container's environment, so staging loads with the credentials in its own
// .env.

const (
	pgUser = `u="${POSTGRES_USER:-${POSTGRESQL_USERNAME:-postgres}}"; ` +
		`db="${POSTGRES_DB:-${POSTGRESQL_DATABASE:-$u}}"; ` +
		`pw="${POSTGRES_PASSWORD:-$POSTGRESQL_PASSWORD}"; ` +
		`[ -n "$pw" ] && export PGPASSWORD="$pw"; `
	mysqlRoot = `cli=mysql; command -v mariadb >/dev/null 2>&1 && cli=mariadb; ` +
		`pw="${MYSQL_ROOT_PASSWORD:-$MARIADB_ROOT_PASSWORD}"; ` +
		`set -- -uroot; [ -n "$pw" ] && set -- "$@" -p"$pw"; `
)

// ReadyCommands exit 0 once the database answers over TCP. The official
// images first start a server on the socket alone to run their init scripts,
// so an answer on 127.0.0.1 means init has finished and the real server is
// up. mysqladmin ping succeeds even when the login is refused, which is
// enough to know the server is listening.
var ReadyCommands = map[string]string{
	"postgres": pgUser + `exec pg_isready -q -h 127.0.0.1 -U "$u" -d "$db"`,
	"mysql": `admin=mysqladmin; command -v mariadb-admin >/dev/null 2>&1 && admin=mariadb-admin; ` +
		mysqlRoot + `exec "$admin" "$@" -h 127.0.0.1 ping --silent`,
}

// CopyDumpCommands dump live's database for a staging copy. The MySQL dump
// leaves out the server's own schemas, whose users and grants staging made
// for itself from its own environment, and fails when there is nothing else
// to dump rather than load an empty copy.
var CopyDumpCommands = map[string]string{
	"postgres": pgUser + `exec pg_dump -w -U "$u" "$db"`,
	"mysql": `dump=mysqldump; command -v mariadb-dump >/dev/null 2>&1 && dump=mariadb-dump; ` + mysqlRoot +
		`dbs=$("$cli" "$@" -N -B -e "SELECT schema_name FROM information_schema.schemata WHERE schema_name NOT IN ` +
		`('mysql','sys','information_schema','performance_schema')") || exit 1; ` +
		`[ -n "$dbs" ] || { echo "no application database to copy" >&2; exit 1; }; ` +
		`exec "$dump" "$@" --single-transaction --routines --triggers --events --databases $dbs`,
}

// LoadCommands read a dump from standard input into the staging copy's
// database and stop at the first error.
var LoadCommands = map[string]string{
	"postgres": pgUser + `exec psql -q -w -v ON_ERROR_STOP=1 -U "$u" -d "$db"`,
	"mysql":    mysqlRoot + `exec "$cli" "$@"`,
}
