# Ignition Server
Ignition server is an HTTP request multiplexer.
It serves Ignition payloads over the /ignition endpoint for a particular "Bearer $token" passed through the "Authorization" Header.

## TokenSecret controller
This is the controller that generates and caches the Ignition payloads served by the Ignition server.
Each NodePool creates a token Secret for a given release/config pair.
The TokenSecret controller watches token Secrets and:
 - Maintain an in memory cache for token/release-config payload pairs.
 - Manage the rotation of any token after the current one has lived TTL/2. This results in both tokens for the same release/config pair coexisting during a TTL/2 duration.
 - Expires and eventually removes any token after the TTL.

i.e a token is active a total of 11 hours, 5.5 main and then 5.5 in the rotated spot.

### Token Deletion 

Token secrets and the associated tokens are revoked/deleted immediately when the corresponding nodePool is deleted. 

When a NodePool is upgraded or goes through a config change, a new token secret is created that corresponds to the updated config value and the old token secret is marked with an expiration date by the NodePool controller. Once the expiration date has passed: the old token secret is deleted and the associated tokens are revoked. This strategy is done to allow in flight provisions that occurred in proximity to the nodePool upgrade to complete.

## Ignition provider
An interface to be implemented to produce a valid ignition payload out of a given release/config pair.

### MachineConfigServer Provider
An Ignition provider implementation which runs pods of the Machine Config Server ephemerally in order to produce a valid ignition payload.
