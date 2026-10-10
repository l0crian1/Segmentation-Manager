# Using Segmentation Manager

[Documentation home](../README.md)

## Connect a VyOS router

Segmentation Manager uses the VyOS HTTPS REST API. Enable it on each managed router before adding the device. Adapt this example to your management addresses and use a strong, unique API key:

```text
configure
set service https api keys id segman key '<STRONG_RANDOM_API_KEY>'
set service https api rest
set service https listen-address '<ROUTER_MANAGEMENT_IP>'
set service https allow-client address '<MANAGER_SOURCE_IP>/32'
commit
save
exit
```

The allowed client must be the source address that the router actually sees, which can differ from the container's address because of NAT or host networking. Keep existing API clients in the allow-list if they still require access. The default HTTPS API port is 443. See [VyOS HTTPS API configuration](https://docs.vyos.io/en/1.5/configuration/service/https.html) for certificate and listener options. API keys provide powerful router access, so protect them and restrict API reachability.

1. Open **Sites** and create a site to group your routers.
2. Open **Devices**, choose **Add device**, and enter its name, site, hostname or IP address, HTTPS port, and API key. Enter the host without a URL scheme or path.
3. Choose certificate trust. System trust checks the router certificate against the container's trusted certificate authorities. For a self-signed or privately issued certificate, [obtain its SHA-256 fingerprint on the router](administration.md#obtaining-a-vyos-certificate-fingerprint), then use it to verify and pin the certificate. Certificate inspection sends no API credentials.
4. Choose whether to save verified changes to the router's startup configuration. This is enabled by default.
5. Save the device and open **VRFs**, **MPLS**, or **VXLAN** to discover its configuration.

To rotate an API key or change an endpoint, edit the device. Leaving its API-key field empty preserves the existing key. Removing a device removes its inventory entry without changing the router. Move or remove a site's devices before deleting that site.

## View and create VRFs

Open **VRFs** and select a router. The list shows each VRF's name, routing table ID, BGP ASN, and L3VNI where configured. Technology badges reflect actual configuration: **MPLS**, **VXLAN**, both badges, or a muted dash for neither.

Choose **Create VRF** and supply:

- **VRF name:** 1–15 characters, starting with a letter; letters, digits, underscores, and hyphens are accepted. Names reserved for interfaces are rejected.
- **Routing table ID:** an unused number from 100 to 65535.
- **Description:** optional.
- **BGP ASN:** required when enabling MPLS VPN or EVPN. Use the ASN appropriate to the router's BGP configuration.

You can create a plain VRF, enable either technology, or enable both.

### MPLS VPN settings

Enable IPv4 and/or IPv6 MPLS VPN. For each enabled family, supply a route distinguisher, one or more explicit export route-targets, and a VPN export label. The label can be `auto` or a number from 16 to 1048575. Choose **Per-Nexthop Label Allocation** when needed; disabling it uses VyOS's default per-VRF allocation.

Each enabled family also has optional **VPN import route-map** and **VPN export route-map** selectors. Choose from the route-maps configured on the selected router, or choose **None** to remove an assignment. IPv4 and IPv6 import/export assignments are independent and work for VRFs that also use EVPN. Changing only an assignment preserves existing route-targets and other VRF settings; route-map definitions remain managed on VyOS.

Refresh the VRFs page after adding or removing route-maps on the router. Existing assignments appear in the editor; an assignment whose definition is missing is labeled accordingly and can be cleared or replaced. Configuration changes made while an editor is open require closing it, refreshing, and reviewing again before applying.

For example, an IPv4 MPLS VRF might use ASN `65000`, RD `65000:101`, export RT `65000:101`, and label `auto`. RDs identify VPN routes; RTs control which VPN routes other VRFs import. Choose these values according to your network's addressing and route-target plan.

The per-nexthop setting corresponds to:

```text
set vrf name USERS protocols bgp address-family ipv4-unicast label vpn allocation-mode per-nexthop
```

### VXLAN/EVPN settings

Enable EVPN and supply an L3VNI from 1 to 16777215, one or more **explicit export route-targets**, and at least one IPv4/IPv6 unicast advertisement family. You may supply an EVPN route distinguisher or leave it for FRR to derive.

For example, an EVPN VRF might use ASN `65000`, L3VNI `10205`, and export RT `65000:205`. The export RT does not have to match the VNI. A missing explicit export RT must be configured before that VRF can participate in the EVPN matrix. You can set it in the VRF editor, or on VyOS:

```text
configure
set vrf name USERS protocols bgp address-family l2vpn-evpn route-target export '65000:205'
commit
save
exit
```

This example adds the explicit target to an otherwise configured EVPN VRF. It is not a complete EVPN deployment recipe. Segmentation Manager supports multiple L3VNIs sharing an external VXLAN interface and leaves that interface and its bridge configuration under your control.

Review the generated commands before applying a new VRF. These commands create the routing instance and selected per-VRF BGP settings. Existing transport, BGP peerings, interfaces, bridges, and route redistribution must be configured separately. See [VyOS VRF configuration](https://docs.vyos.io/en/1.5/configuration/vrf/index.html) for those routing details.

## Edit or delete a VRF

Select an existing VRF to edit its description, ASN, and supported MPLS/EVPN settings. The editor reads the current configuration, including per-nexthop allocation. Existing VRF names and table IDs are fixed; changing them requires recreation.

Ordinary edits preserve import route-targets. Use the connectivity matrices to change those imports. Disabling a technology requires acknowledging the removal of its import/export settings, and the command preview shows the affected paths.

Deleting a VRF requires typing its exact name. The application blocks deletion when it discovers external references, such as assigned interfaces, service bindings, or imports from another VRF. Detach those dependencies on VyOS first, then refresh and review again. Deletion removes the VRF and routing configuration inside it; it does not cascade into unrelated interfaces or other external resources.

## Manage connectivity

Open **MPLS** or **VXLAN** and select a router. For MPLS, also choose IPv4 or IPv6; each family has its own import policy. The VXLAN page manages the VRF's EVPN route-target imports.

A matrix describes the selected router's configured route-target relationships. It does not configure another PE automatically or verify forwarding and firewall behavior.

### Between different VRFs

Selecting an off-diagonal tile stages a bidirectional relationship. If `USERS` exports `65000:101` and `SERVICES` exports `65000:102`, connecting them imports `65000:102` into `USERS` and `65000:101` into `SERVICES`. The mirrored tile represents the same staged change.

The matrix identifies complete, partial, and absent relationships. An existing asymmetric relationship may appear partial; the editor lets you complete or disconnect the pair rather than configure two directions independently. Removing a relationship removes the corresponding peer export targets while preserving unrelated imports, subject to shared-target safety checks.

### Same-VRF imports between PEs

The diagonal tile lets a VRF import its own export targets. If `USERS` on two PEs exports `65000:101`, enabling the `USERS` diagonal on each PE allows each instance to import routes carrying that target from the other PE. This is supported in both MPLS and EVPN matrices.

Configure each PE through its own device view. A change on the selected router does not apply to the remote PE.

### Review and apply

1. Refresh the matrix to load the router's current state.
2. Select the desired relationships. Changes remain staged until applied.
3. Review the command preview and warnings, including whether the result will be saved.
4. Apply and wait for the operation to finish. Large changes can take several minutes.
5. Check the verified result and any save warning. Use **Settings → Activity** to inspect operation history.

The application continues an accepted operation even if the browser disconnects. Reloading resumes tracking. Do not submit the same change again just because a request appeared to time out; inspect the recorded result and refresh first.

## Synchronization and saving

Refresh discovers VRFs and policy changes made directly on VyOS. Matrices also refresh at the interval configured in Settings while not editing or applying changes. After VRF changes, refresh the relevant matrix to see the new state.

Before applying, Segmentation Manager checks for configuration changes since your view was loaded. If it asks you to refresh, review the new configuration and stage your intended changes again. Avoid concurrent CLI or other API configuration work on the same router during an application change.

After committing, the application reads the router configuration back to verify the result. With the device's save option enabled, it then saves the **entire running configuration**, including any unrelated unsaved changes. With saving disabled, the verified changes remain in running configuration only and can be lost at reboot.

If verification or saving fails, inspect the operation and the router before deciding how to recover. The application does not automatically roll back router configuration or blindly retry an uncertain commit. See [troubleshooting](compatibility.md#troubleshooting).
