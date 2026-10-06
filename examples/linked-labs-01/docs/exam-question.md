# Linked labs: log in against another cluster

This lab has **two kind clusters**: yours, and an identity provider (`idp`) in its own cluster next to it. Your cluster can reach it, but not through a Kubernetes Service name — it's a different cluster.

## Task

In your cluster (`astrona shell linked-labs-01`), in namespace `default`:

1. Find out where the identity provider is. Astrona publishes it in the ConfigMap `astrona-links`.
2. Create a **Job named `login`** (image `curlimages/curl:8.11.1`) that requests `http://<idp host>:30080/token` and prints the response.
3. The Job must complete, and its log must contain the token.

Leave the identity provider itself running — it's graded too.

Check your work with `astrona submit` (or `astrona submit --watch` while you work).
