# hccdn-cli

A small CLI for uploading files and image variants to the [Hack Club CDN](https://cdn.hackclub.com/).

It keeps a local SQLite index of uploads. Uploading an unchanged file again reuses the existing CDN URL while still creating a fresh session and JSON manifest. Files are identified by SHA-256 content hashes, not only by their paths.

## Install

Build from source:

```sh
go build .
```

Or download a Linux amd64 or macOS arm64 archive from GitHub Releases and put `hccdn-cli` on your `PATH`.

Create an API key in the Hack Club CDN dashboard and export it:

```sh
export HCCDN_API_KEY=sk_cdn_your_key_here
```

A local `.env` file containing `HCCDN_API_KEY=...` is also supported. The key is never written to output or the database.

## Upload

Upload a file or the immediate files in a directory:

```sh
hccdn-cli up ./photos
```

Upload recursively:

```sh
hccdn-cli up ./photos --recursive
```

Generate original and WebP variants:

```sh
hccdn-cli up ./photos --optimise none,full,300,720
```

Variant values are:

- `none`: upload the original bytes.
- `full`: encode an 85-quality WebP at the original dimensions.
- A positive number such as `720`: encode an 85-quality WebP whose longest side is at most that many pixels. Images are never upsampled.

PNG and JPEG extensions are matched case-insensitively. Other file types are uploaded unchanged. Existing `*.hccdn.json` files are always excluded from directory uploads.

By default, uploads use at most four workers, retry transient CDN failures twice, and time out each request after two minutes. These are configurable:

```sh
hccdn-cli up ./photos --workers 2 --retries 3 --timeout 5m
```

### Manifests and output

A successful directory upload writes `<session-id>.hccdn.json` inside the directory. A single file writes `<file>.hccdn.json`. The manifest contains every requested result, whether newly uploaded or reused.

Choose another destination or write JSON to stdout:

```sh
hccdn-cli up ./photos --output ./photos.json
hccdn-cli up ./photos --output -
hccdn-cli up ./photos --json
```

Normal output is one summary line. Use `--verbose` for per-variant activity or `--quiet` for errors only:

```sh
hccdn-cli --verbose up ./photos
hccdn-cli --quiet up ./photos
```

Manifests retain the original absolute `path` field for compatibility and also include a portable `relative_path`. Each upload includes its optimisation setting, source and payload hashes, and whether it was reused.

## Duplicate detection and database upgrades

The cache identity combines:

- SHA-256 of the source file.
- A versioned transformation recipe such as `webp:v1:q85:max=720`.

Changing one file uploads only that file's requested variants. Adding an optimisation size uploads only the missing size. Identical content at a different path can reuse the same CDN object.

Databases made by older releases are migrated automatically in place after a one-time `<database>.v1.bak` backup is created. Migration only adds schema and references; it does not eagerly read or hash files. When a legacy file/variant is first requested again, the CLI generates its requested payload, hashes the existing public CDN object, and reuses/backfills the old record only when the bytes match. Changed legacy files are uploaded as new objects.

The database is stored at:

- macOS and Windows: the platform user config directory under `hccdn-cli/hccdn.db`.
- Linux with `XDG_CONFIG_HOME`: `$XDG_CONFIG_HOME/hccdn-cli/hccdn.db`.
- Other Linux setups: `~/.local/share/hccdn-cli/hccdn.db`.

`HCCDN_DB_PATH` can override the location, which is useful for isolated automation.

## History and status

Show recent sessions or inspect one in detail:

```sh
hccdn-cli history
hccdn-cli history --limit 50
hccdn-cli history AbCdEfGh
hccdn-cli history --json
```

Show the local database counts and, when an API key is available, current CDN quota:

```sh
hccdn-cli status
hccdn-cli status --json
```

## Delete

Preview a deletion first:

```sh
hccdn-cli rm ./photos --dry-run
```

Delete references belonging to a file, directory, or session:

```sh
hccdn-cli rm ./photos/image.jpg
hccdn-cli rm ./photos
hccdn-cli rm AbCdEfGh
```

Files do not need to remain on disk for path deletion to work. Directory matching respects path boundaries and includes descendants.

Uploads reused by another session/path are unlinked but kept on the CDN. A remote object is deleted only after its final active local reference is removed. History is retained and marks removed sessions accordingly.

Deleting everything requires confirmation. For non-interactive use, pass `--yes`:

```sh
hccdn-cli rm all
hccdn-cli rm all --yes
```

## Development

```sh
go test ./...
go test -race ./...
go vet ./...
```

Releases are built for Linux amd64 and macOS arm64 when a version tag is pushed.

## License

MIT. See [LICENSE](LICENSE).
