# SPDX-License-Identifier: GPL-3.0-or-later
#
# Fills templates/sing-box.json.in. Compatible with jq 1.6 (Debian 12).
#   jq --arg secret S --arg dns 1.1.1.1 --arg profile default
#      --argjson selfips '["192.168.1.7"]' --argjson failover false
#      --argjson probe_base 22101 --slurpfile exits exits.json
#      -f sing-box.jq sing-box.json.in
# exits.json: one array of {name, kind: "outbound"|"endpoint", server, obj}.
# Each exit becomes an outbound (or WireGuard endpoint) tagged "<name>-<profile>",
# the naming Junction's failover expects. Selectors: "vpn-active" (global; devices
# in vpn-devices.json) and "vpn-<profile>" (devices in profile-<profile>.json).

($exits[0] // []) as $ex
| $profile as $p
| [$ex[] | "\(.name)-\($p)"] as $tags
| ($tags + ["direct"]) as $choices
| (if ($tags | length) > 0 then $tags[0] else "direct" end) as $default
| def selector($tag): {type: "selector", tag: $tag, outbounds: $choices, default: $default, interrupt_exist_connections: true};

  .experimental.clash_api.secret = $secret
| .dns.servers[0].server = $dns
| (.route.rules[] | select(.action == "reject" and has("port")) | .ip_cidr) += [$selfips[] | "\(.)/32"]
| .outbounds = ([$ex[] | select(.kind == "outbound") | .obj + {tag: "\(.name)-\($p)"}]
               + [selector("vpn-active"), selector("vpn-\($p)")]
               + .outbounds)
| (if any($ex[]; .kind == "endpoint")
   then .endpoints = [$ex[] | select(.kind == "endpoint") | .obj + {tag: "\(.name)-\($p)"}]
   else . end)
| .route.rule_set += [{type: "local", tag: "profile-\($p)", format: "source", path: "/etc/sing-box/profile-\($p).json"}]
# Per-profile rule just before the global vpn-devices rule.
| .route.rules = (.route.rules[:-1] + [{rule_set: ["profile-\($p)"], outbound: "vpn-\($p)"}] + .route.rules[-1:])
| (if $failover then
     .inbounds += [$ex | to_entries[] | {type: "mixed", tag: "probe-\(.value.name)-\($p)", listen: "127.0.0.1", listen_port: ($probe_base + .key)}]
     # Probe traffic must reach its exit no matter what: right after the loop guard.
   | .route.rules = (.route.rules[:3] + [$ex[] | {inbound: ["probe-\(.name)-\($p)"], outbound: "\(.name)-\($p)"}] + .route.rules[3:])
   else . end)
