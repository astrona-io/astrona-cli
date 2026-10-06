# Step-by-step solution

```sh
astrona shell linked-labs-01

# 1. The identity provider has a stable name in your cluster
kubectl run dns --image=curlimages/curl:8.11.1 --rm -i --restart=Never -- nslookup idp.astrona.internal

# 2. A Job that logs in against it
kubectl create job login --image=curlimages/curl:8.11.1 -- curl -fsS http://idp.astrona.internal:30080/token
kubectl wait job/login --for=condition=Complete --timeout=120s
kubectl logs job/login     # {"access_token": "lab-token-42", ...}

exit
astrona submit
```

Why it works: every kind cluster runs on the same container network, so a pod in your cluster can reach the idp cluster's node. Astrona points `idp.astrona.internal` at that node in your cluster's DNS. The idp publishes its Service as a **NodePort** (30080) — a `ClusterIP` Service or a pod IP of the other cluster wouldn't be reachable.

You can look at the other side too: `kubectl --context "$ASTRONA_CLUSTER_IDP_CONTEXT" -n auth get pods,svc` works inside `astrona shell linked-labs-01`.
