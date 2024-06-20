# Monica

Monica is designed to handle Network requests and responses. 

## Features
* Parse raw HTTP requests and return a Request object.
* Send HTTP requests with custom headers and body.
* Parse HTTP responses, including headers and body.

## Usage
* Runs all `mon` files in the current directory
	```bash
	monica run 
	```
* Run a single file 
	```bash
	monica run -s <file>
	```

## Mon file format
 Lines starting with `#` are considered comments and are ignored.
### HTTP Request
```
#Comment
HTTP METHOD URL
HEADERS

BODY
```

#### Example
```
# Simple HTTP Request
GET http://httpbin.org/status/200
Host: httpbin.org
User-Agent: monica/0.1.0

```

```
# Simple POST HTTP Request
GET http://httpbin.org/anything
Host: httpbin.org
User-Agent: monica/0.1.0

{
  "key": "value"
}
```
