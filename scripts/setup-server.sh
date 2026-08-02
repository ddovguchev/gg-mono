#!/bin/bash
# ──────────────────────────────────────────────────────────────────
# mono-go Server Setup
# Устанавливает ВСЕ локальные модели на dd@10.10.0.8 (RTX 5060 Ti 16GB)
# ──────────────────────────────────────────────────────────────────

set -e

echo "═══════════════════════════════════════════════════════════════"
echo "  mono-go — Local Model Server Setup"
echo "  Target: RTX 5060 Ti 16GB"
echo "═══════════════════════════════════════════════════════════════"

# ─── 1. System dependencies ──────────────────────────────────────

echo ""
echo "▶ [1/6] Installing system dependencies..."

sudo apt-get update
sudo apt-get install -y \
    python3 python3-pip python3-venv \
    ffmpeg \
    portaudio19-dev \
    curl wget git

# ─── 2. Ollama (LLM) ────────────────────────────────────────────

echo ""
echo "▶ [2/6] Installing Ollama..."

if ! command -v ollama &> /dev/null; then
    curl -fsSL https://ollama.com/install.sh | sh
else
    echo "  Ollama already installed: $(ollama --version)"
fi

# Запускаем Ollama как сервис
sudo systemctl enable ollama
sudo systemctl start ollama

echo "  Pulling llama3 model..."
ollama pull llama3

echo "  Ollama ready at http://localhost:11434"

# ─── 3. Whisper (Speech-to-Text) ────────────────────────────────

echo ""
echo "▶ [3/6] Installing Whisper (faster-whisper)..."

WHISPER_DIR="/opt/whisper-server"
sudo mkdir -p $WHISPER_DIR

sudo tee $WHISPER_DIR/requirements.txt > /dev/null << 'EOF'
faster-whisper==1.1.1
flask==3.1.1
flask-cors==5.0.1
gunicorn==23.0.0
EOF

sudo python3 -m venv $WHISPER_DIR/venv
sudo $WHISPER_DIR/venv/bin/pip install -r $WHISPER_DIR/requirements.txt

# Whisper HTTP сервер
sudo tee $WHISPER_DIR/server.py > /dev/null << 'PYEOF'
"""Whisper ASR HTTP Server — OpenAI-compatible API."""
import io
import tempfile
from flask import Flask, request, jsonify
from flask_cors import CORS
from faster_whisper import WhisperModel

app = Flask(__name__)
CORS(app)

# large-v3 на RTX 5060 Ti (16GB) — float16, быстрый и точный
model = WhisperModel("large-v3", device="cuda", compute_type="float16")

@app.route("/health", methods=["GET"])
def health():
    return jsonify({"status": "ok", "model": "large-v3"})

@app.route("/v1/audio/transcriptions", methods=["POST"])
def transcribe():
    if "file" not in request.files:
        return jsonify({"error": "No file provided"}), 400

    file = request.files["file"]
    language = request.form.get("language", None)

    # Сохраняем во временный файл
    with tempfile.NamedTemporaryFile(suffix=".wav", delete=False) as tmp:
        file.save(tmp.name)
        segments, info = model.transcribe(
            tmp.name,
            language=language,
            beam_size=5,
            vad_filter=True,
        )
        text = " ".join(seg.text.strip() for seg in segments)

    return jsonify({"text": text})

if __name__ == "__main__":
    app.run(host="127.0.0.1", port=8000)
PYEOF

# Systemd сервис для Whisper
sudo tee /etc/systemd/system/whisper.service > /dev/null << EOF
[Unit]
Description=Whisper ASR Server
After=network.target

[Service]
Type=simple
User=$USER
WorkingDirectory=$WHISPER_DIR
ExecStart=$WHISPER_DIR/venv/bin/python server.py
Restart=always
RestartSec=5
Environment=CUDA_VISIBLE_DEVICES=0

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable whisper
sudo systemctl start whisper

echo "  Whisper ready at http://localhost:8000"

# ─── 4. Coqui TTS (Text-to-Speech) ──────────────────────────────

echo ""
echo "▶ [4/6] Installing Coqui TTS (XTTS v2)..."

TTS_DIR="/opt/tts-server"
sudo mkdir -p $TTS_DIR

sudo tee $TTS_DIR/requirements.txt > /dev/null << 'EOF'
TTS==0.25.3
flask==3.1.1
flask-cors==5.0.1
gunicorn==23.0.0
soundfile==0.13.1
EOF

sudo python3 -m venv $TTS_DIR/venv
sudo $TTS_DIR/venv/bin/pip install -r $TTS_DIR/requirements.txt

# TTS HTTP сервер
sudo tee $TTS_DIR/server.py > /dev/null << 'PYEOF'
"""Coqui TTS HTTP Server — XTTS v2 with voice cloning."""
import io
import base64
import tempfile
import soundfile as sf
from flask import Flask, request, jsonify, Response
from flask_cors import CORS

app = Flask(__name__)
CORS(app)

# Загружаем XTTS v2 ( ~4GB VRAM)
from TTS.api import TTS
tts = TTS("tts_models/multilingual/multi-dataset/xtts_v2").to("cuda")

@app.route("/health", methods=["GET"])
def health():
    return jsonify({"status": "ok", "model": "xtts_v2"})

@app.route("/api/tts", methods=["POST"])
def synthesize():
    data = request.get_json()
    text = data.get("text", "")
    language = data.get("language", "en")

    if not text:
        return jsonify({"error": "No text provided"}), 400

    # Генерируем аудио
    with tempfile.NamedTemporaryFile(suffix=".wav", delete=False) as tmp:
        tts.tts_to_file(
            text=text,
            file_path=tmp.name,
            language=language,
        )
        # Читаем WAV и возвращаем
        audio_data, sample_rate = sf.read(tmp.name)

    # Конвертируем в WAV bytes
    buf = io.BytesIO()
    sf.write(buf, audio_data, sample_rate, format="WAV")
    buf.seek(0)

    return Response(
        buf.getvalue(),
        mimetype="audio/wav",
        headers={"Content-Disposition": "attachment; filename=output.wav"}
    )

if __name__ == "__main__":
    app.run(host="127.0.0.1", port=5002)
PYEOF

# Systemd сервис для TTS
sudo tee /etc/systemd/system/tts.service > /dev/null << EOF
[Unit]
Description=Coqui TTS Server (XTTS v2)
After=network.target

[Service]
Type=simple
User=$USER
WorkingDirectory=$TTS_DIR
ExecStart=$TTS_DIR/venv/bin/python server.py
Restart=always
RestartSec=5
Environment=CUDA_VISIBLE_DEVICES=0

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable tts
sudo systemctl start tts

echo "  TTS ready at http://localhost:5002"

# ─── 5. SSH Key (for passwordless access) ───────────────────────

echo ""
echo "▶ [5/6] Setting up SSH access..."

if [ ! -f ~/.ssh/id_ed25519 ]; then
    echo "  Generating SSH key..."
    ssh-keygen -t ed25519 -f ~/.ssh/id_ed25519 -N ""
fi

echo "  Your public key (add to authorized_keys on Mac):"
echo ""
cat ~/.ssh/id_ed25519.pub
echo ""

# ─── 6. Firewall ────────────────────────────────────────────────

echo ""
echo "▶ [6/6] Configuring firewall..."

# Ollama: только localhost (SSH tunnel)
# Whisper: только localhost (SSH tunnel)
# TTS: только localhost (SSH tunnel)
# Никаких внешних портов не нужно — всё через SSH

echo ""
echo "═══════════════════════════════════════════════════════════════"
echo "  ✅ Server setup complete!"
echo ""
echo "  Services:"
echo "    • Ollama (LLM):     http://localhost:11434"
echo "    • Whisper (ASR):    http://localhost:8000"
echo "    • Coqui TTS (XTTS): http://localhost:5002"
echo ""
echo "  Models:"
echo "    • Ollama: llama3 (8B, ~5GB VRAM)"
echo "    • Whisper: large-v3 (~4GB VRAM)"
echo "    • TTS: XTTS v2 (~4GB VRAM)"
echo ""
echo "  Total VRAM usage: ~13GB (fits in 16GB)"
echo ""
echo "  Next steps:"
echo "    1. Copy your Mac's SSH public key here"
echo "    2. Run mono-go on your Mac"
echo "═══════════════════════════════════════════════════════════════"
