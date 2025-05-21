Add a new node:

```
curl -X POST http://localhost:8080/document1 -H "Content-Type: application/json" -d '{
  "id": "node1",
  "data": "First node data",
  "parents": []
}'
```

Add another node with an edge pointing to the first node:

```
curl -X POST http://localhost:8080/document1 -H "Content-Type: application/json" -d '{
  "id": "node2",
  "data": "Second node data",
  "parents": ["node1"]
}'
```

Get nodes that were added after a specific node:

```
curl -X GET "http://localhost:8080/document1?after=node1"
```
