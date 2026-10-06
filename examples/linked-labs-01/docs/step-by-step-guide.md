# Step-by-step solution

```sh
astrona shell linked-labs-01

# 1. Where is the identity provider?
kubectl get configmap astrona-links -o jsonpath='{.data.idp\.host}'
# → astro-linked-labs-01-idp-control-plane

# 2. A Job that logs in against it
IDP=$(kubectl get configmap astrona-links -o jsonpath='{.data.idp\.host}')
kubectl create job login --image=curlimages/curl:8.11.1 -- curl -fsS "http://$IDP:30080/token"
kubectl wait job/login --for=condition=Complete --timeout=120s
kubectl logs job/login     # {"access_token": "lab-token-42", ...}

exit
astrona submit
```

Why it works: every kind cluster runs on the same container network, so a pod in your cluster can reach the idp cluster's node by its container name. The idp publishes its Service as a **NodePort** (30080) — a `ClusterIP` Service or a pod IP of the other cluster wouldn't be reachable.

You can look at the other side too: `kubectl --context "$ASTRONA_LINK_IDP_CONTEXT" -n auth get pods,svc` works inside `astrona shell linked-labs-01`.
