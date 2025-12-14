#!/usr/bin/env python3
"""
OCR Server - PaddleOCR + EasyOCR
Запуск: .venv/bin/python scripts/ocr_server.py

POST /ocr {"path": "/tmp/image.png", "engine": "paddle"}  # или "easy"
"""
import sys
import json
from http.server import HTTPServer, BaseHTTPRequestHandler

# Загружаем оба движка
print("Loading PaddleOCR...", file=sys.stderr)
from paddleocr import PaddleOCR
paddle_ocr = PaddleOCR(use_textline_orientation=True, lang='ru')
print("PaddleOCR ready!", file=sys.stderr)

print("Loading EasyOCR...", file=sys.stderr)
import easyocr
easy_ocr = easyocr.Reader(['ru', 'en'], gpu=False, verbose=False)
print("EasyOCR ready!", file=sys.stderr)


def ocr_paddle(image_path):
    result = paddle_ocr.ocr(image_path, cls=True)
    texts = []
    if result and result[0]:
        for line in result[0]:
            if line and len(line) >= 2:
                texts.append(line[1][0])
    return ' '.join(texts).strip()


def ocr_easy(image_path):
    results = easy_ocr.readtext(image_path)
    return ' '.join([item[1] for item in results]).strip()


class OCRHandler(BaseHTTPRequestHandler):
    def log_message(self, format, *args):
        pass

    def do_POST(self):
        if self.path != '/ocr':
            self.send_error(404)
            return

        content_length = int(self.headers.get('Content-Length', 0))
        body = self.rfile.read(content_length).decode('utf-8')

        try:
            data = json.loads(body)
            image_path = data.get('path', '')
            engine = data.get('engine', 'paddle')

            if not image_path:
                self.send_error(400, 'Missing path')
                return

            if engine == 'easy':
                text = ocr_easy(image_path)
            else:
                text = ocr_paddle(image_path)

            response = json.dumps({'text': text, 'engine': engine})
            self.send_response(200)
            self.send_header('Content-Type', 'application/json')
            self.end_headers()
            self.wfile.write(response.encode('utf-8'))

        except Exception as e:
            self.send_response(500)
            self.send_header('Content-Type', 'application/json')
            self.end_headers()
            self.wfile.write(json.dumps({'error': str(e)}).encode('utf-8'))

    def do_GET(self):
        if self.path == '/health':
            self.send_response(200)
            self.send_header('Content-Type', 'text/plain')
            self.end_headers()
            self.wfile.write(b'ok (paddle + easy)')
        else:
            self.send_error(404)

if __name__ == '__main__':
    port = 8765
    host = '0.0.0.0'
    server = HTTPServer((host, port), OCRHandler)
    print(f"OCR server (paddle + easy) running on http://{host}:{port}", file=sys.stderr)
    server.serve_forever()
