apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  annotations:
    higress.io/destination: {{.Name}}.DEFAULT-GROUP.public.nacos
{{- if .EnableGrpc}}
    higress.io/backend-protocol: "GRPC"
{{- end}}
  name: {{.Name}}
spec:
  ingressClassName: higress
  rules:
  - http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          resource:
            apiGroup: networking.higress.io
            kind: McpBridge
            name: default
