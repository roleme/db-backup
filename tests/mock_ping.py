from http.server import BaseHTTPRequestHandler, HTTPServer


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        print(f"PING {self.command} {self.path}", flush=True)
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b"OK")

    do_POST = do_GET

    def log_message(self, *args):
        pass


HTTPServer(("0.0.0.0", 8080), Handler).serve_forever()
