# Commands to try `monica import curl` with (from the examples folder):
#
#   monica import curl "$(sed -n 's/^curl /curl /p' 12-import/curl.sh | head -1)"
#
# or paste any of these after `monica import curl '...'`.

curl -X POST https://httpbin.org/post -H 'Content-Type: application/json' -H 'Authorization: Bearer abc' --data-raw '{"name":"ada"}'
curl -G https://httpbin.org/get --data-urlencode 'q=hello world' -d page=2 -sSL
curl https://httpbin.org/post -F title='Report' -F 'file=@04-upload/files/report.csv;type=text/csv'
curl -u ada:secret https://httpbin.org/basic-auth/ada/secret -m 5
