# Contributing

Thanks for helping. Junction runs on people's home gateways, so the bar is: **it must never cut the internet**, and every change has to keep it that way.

## Ground rules

- **Fail open.** If Junction, sing-box or a rule breaks, traffic must still flow (direct, without the VPN). Nothing Junction does may be required for packets to move.
- **No surprises on the network.** Changes to firewall rules, routing or DHCP need a test that shows what's added and removed, and an uninstall path.
- **Plain language in the UI.** Write for someone who isn't a network engineer: say what happened and what to do, not error codes.
- **One look.** Every colour lives in `web/src/theme.css` (dark and light); components only use the tokens.

## Development

Requirements: Go (see `go.mod`), Node 20+.

```sh
cd web && npm ci && npm run dev:mock   # the UI on sample data, no gateway needed
go test ./...                          # backend tests
./build.sh                             # web + linux binary into ./out/
scripts/release.sh                     # release tarballs for amd64, arm64, armv7
```

`npm run dev` (without `:mock`) proxies `/api` to a real gateway; set its address in `web/vite.config.ts`.

## Pull requests

- Keep them focused; explain the user-visible change and how you tested it.
- Run `gofmt -l .`, `go vet ./...`, `go test ./...` and `cd web && npx tsc --noEmit -p .` before pushing.
- UI changes: include a screenshot in dark and light mode, and one on a phone width.
- Don't commit secrets or anything from your own network (MAC addresses, device names, IPs other than documentation ranges).

By contributing you agree your work is licensed under the GPL-3.0-or-later, like the rest of the project.

## Keeping your own network out of commits

Develop on your own gateway as much as you like, but keep its details out of the repository: put your deploy target and files in a site folder outside the repo (`../junction-site`, see `build.sh`). To be safe, list what must never be published (names, MAC addresses, home IPs, account numbers), one regex per line, in `~/.junction-private`, then run `scripts/install-hooks.sh` once: every `git push` is checked against it.
