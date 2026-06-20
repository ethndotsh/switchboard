from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        body = {
            "path": self.path,
            "rule": self.headers.get("x-switchboard-rule", ""),
        }
        data = json.dumps(body, sort_keys=True).encode()
        self.send_response(200)
        self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def log_message(self, fmt, *args):
        print(fmt % args, flush=True)


ThreadingHTTPServer(("0.0.0.0", 9000), Handler).serve_forever()
