apiVersion: networking.higress.io/v1
kind: McpBridge
metadata:
  name: default
  namespace: higress-system
spec:
  registries:
  - name: {{.Name}}-nacos
    type: nacos2
    domain: 127.0.0.1
    port: 8848
    nacosNamespaceId: public
    nacosGroups:
    - DEFAULT_GROUP
