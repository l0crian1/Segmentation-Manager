# Compatibility and troubleshooting

[Documentation home](../README.md)

## VyOS compatibility

All VyOS release versions, including LTS, rolling, development, and custom builds, can use VRF creation, editing, deletion, and the MPLS/EVPN matrices. The reported version is informational; there is no version allow-list or version-based read-only mode.

The router must expose the HTTPS API and accept the generated configuration paths. Configuration validation, account permissions, dependency checks, stale-state detection, and read-back verification still apply. Allowing a release does not imply that every feature has been tested on that release.

Live configuration tests on **VyOS 1.4.4 and 1.5.1** cover plain and combined MPLS/EVPN VRF creation, editing and deletion; IPv4/IPv6 MPLS and EVPN pair and same-VRF imports; preservation of imports during VRF edits; all four VPN route-map assignments; and IPv4/IPv6 per-nexthop label allocation. Temporary test resources were removed and each router's full baseline configuration was verified restored without saving. These are configuration tests, not end-to-end forwarding tests.

Earlier live validation also covered `2026.09.17-0028-rolling`. Automated regression tests ensure arbitrary LTS, rolling, custom, future, and unknown version strings cannot disable VRF or matrix editing.

Hosting the application through VyOS native containers is documented for 1.5. This hosting requirement is separate from the versions it can manage remotely. Published images target Linux AMD64 and ARM64.

## Supported policy model

MPLS matrices manage explicit import RTs separately for IPv4 and IPv6. EVPN matrices use explicitly configured export RTs and manage explicit import RTs. Both support same-VRF diagonal imports for routes originating on other PEs.

The application supports EVPN VRFs using multiple L3VNIs on a shared external VXLAN interface. It does not configure interfaces, bridges, global BGP peerings, transport, or redistribution.

Automatic EVPN target discovery is not supported. A VRF without explicit EVPN export targets needs them configured before editing its matrix relationships. Even with explicit exports, FRR can derive automatic imports when no explicit imports exist. Those automatic imports are outside the matrix's view; adding or removing the last explicit import can change route import behavior. See [FRR EVPN behavior](https://docs.frrouting.org/en/latest/evpn.html).

Wildcard route-targets, combined `both` target leaves, and VRF shortcut leaking can make a VRF read-only in the connectivity matrices where the application cannot safely express an import-only change.

VPN import/export route-map assignments do not make a VRF read-only. Matrix changes update explicit import route-targets while preserving those assignments. The VRFs page supports assigning existing VPN route-maps independently for IPv4 and IPv6. Route-map rules can further filter or modify routes, so the matrix represents configured route-target relationships rather than the effective result of those rules.

Shared export targets can affect more than the selected pair of VRFs. The application rejects changes that would alter other discovered pairs unexpectedly. Targets may also match routes from remote routers outside the current view. A matrix is a representation of import policy, not proof of traffic reachability or firewall isolation.

## Configuration operations

Only one application configuration operation runs at a time for a device. A fresh configuration check prevents applying edits against stale state. This cannot lock out external CLI sessions or other API clients; schedule changes so that only one writer is active on a router at a time.

Large commits have a separate ten-minute request allowance and read-back verification can continue for up to two minutes, within an overall operation limit. A slow or failed API response does not by itself establish whether the router committed. The application checks the resulting configuration without blindly resending the change.

If the application restarts during an unfinished operation, the operation is recorded as unresolved. Router configuration can also fail partially. There is no automatic whole-router rollback: inspect the recorded result and the actual router state before recovery.

## Troubleshooting

### The browser cannot connect

Use `https://`, not plain HTTP. Check container status and logs, the published host address and port, and management-network firewall rules. Docker examples default to loopback access; replace that binding with the host's management IP for remote browsers. On VyOS, the host-network deployment listens at port 8443 on the router.

### The container reports a storage permission error

Ensure its persistent directory exists, is writable by UID/GID 10001, and is mounted at `/data` unless you explicitly changed `DATA_DIR`. Check SELinux labeling when using a bind mount. Do not fix this by making stored credentials world-readable.

### Setup appears on an existing installation

Check that the container is using the original volume or bind mount. A changed Compose directory, project name, or mount path can select empty storage. Reconnect the original storage before initializing a second workspace.

### Router connection or certificate failure

Confirm that the API is enabled, the saved port and key are correct, and the API allow-list permits the source address seen from the container. Check DNS, routes, firewall rules, and time synchronization. For a certificate error, verify the router certificate's dates and trust mode; renew or re-enroll a changed certificate as appropriate.

### A VRF is read-only or missing from a matrix

Read its attention message. Check the selected MPLS address family and whether the VRF has suitable explicit export RTs. EVPN requires explicit exports even if FRR is generating automatic targets. Use the VRFs page to inspect its actual configuration, then refresh after correcting it. The router's release version does not make a VRF read-only.

### “Router configuration changed while you were editing”

Refresh the router's current state and review it before restaging changes. Another operator, a separate API client, or a recently completed operation may have changed the baseline. If an operation is still running, allow it to finish before submitting another one.

### A large apply seems stuck or a request timed out

Check **Settings → Activity** and the operation status. An accepted operation runs independently of the browser request. Wait for its final status and refresh before deciding whether any change is still needed. Increasing the general device read timeout does not change the commit timeout.

### Changes applied, but saving failed

The running configuration may already contain the verified changes. Inspect the operation and router, then save through VyOS if appropriate. Remember that saving persists all current configuration, including other operators' unsaved work.

### Routes do not appear despite a selected tile

Check the remote PE's exports, BGP VPN/EVPN sessions, route advertisements, and transport. Inspect the router's actual routing tables. The matrix does not display live BGP routes or runtime VNI status, and a route-target policy alone does not create end-to-end connectivity.
