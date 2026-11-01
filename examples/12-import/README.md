# Importing

Sources to try the importers on (they are not `.mon` files, so `monica run`
ignores them):

```bash
# curl: one of the commands in curl.sh
monica import curl 'curl -X POST https://httpbin.org/post -H "Content-Type: application/json" --data-raw "{\"name\":\"ada\"}"'

# Postman collection (export: Collection v2.1)
monica import postman 12-import/postman/todo.postman_collection.json -o /tmp/todo-postman
cd /tmp/todo-postman && monica run -r 3

# Bruno collection (the folder with bruno.json)
monica import bruno 12-import/bruno/todo -o /tmp/todo-bruno
cd /tmp/todo-bruno && monica run -r 3 --env public

# JetBrains HTTP Client / VS Code REST Client (.http / .rest files or a folder)
monica import http 12-import/http -o /tmp/todo-http
cd /tmp/todo-http && monica run -r 3
```

Each import writes `IMPORT_REPORT.md` with whatever needs a look.
