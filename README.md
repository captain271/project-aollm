# project-aollm

An AI-powered AIM bot that connects to OSCAR-compatible servers and responds to instant messages using a local LLM (like KoboldCPP, LM Studio, or Ollama).

## Features

- ✅ Connects to OSCAR/AIM protocol servers
- ✅ Responds to instant messages using your local LLM
- ✅ 2000s AIM-style persona (lowercase, emoticons, slang)
- ✅ Conversation memory (last 20 messages per user)
- ✅ Realistic typing delay simulation
- ✅ HTML tag stripping from incoming messages

## Requirements

- [Go 1.21+](https://go.dev/doc/install) - Programming language
- A local LLM server with OpenAI-compatible API (KoboldCPP, LM Studio, Ollama, etc.)
- An OSCAR-compatible AIM server (like [open-oscar-server](https://github.com/mk6i/open-oscar-server))

## Quick Start

### 1. Set Up Your LLM

**Option A: KoboldCPP (Recommended)**
```bash
# Start KoboldCPP with your model on port 5001
koboldcpp.exe --model your-model.gguf --port 5001
```

**Option B: LM Studio**
1. Download LM Studio from https://lmstudio.ai
2. Load a model and start the local server (default port 1234)
3. Update config.json URL to `http://localhost:1234/v1/`

**Option C: Ollama**
```bash
ollama pull llama3.2
ollama serve
```
Then change config.json URL to `http://localhost:11434/api/chat`

### 2. Configure the Bot

Edit `config.json`:

```json
{
  "aim": {
    "screenName": "AOLLM",
    "password": "botpass123",
    "serverAddr": "localhost:5190",
    "profile": "AOLLM - AI chatbot from 2003",
    "awayMessage": "hey im a 2003 aim bot lol send me msgs :P"
  },
  "llm": {
    "useLocal": true,
    "local": {
      "url": "http://localhost:5001/v1/",
      "model": "your-model-name"
    }
  },
  "typingDelay": {
    "minMs": 1000,
    "maxMs": 3000,
    "perCharMs": 50
  }
}
```

**Configuration Options:**

| Field | Description |
|-------|-------------|
| `screenName` | The bot's AIM screen name |
| `password` | Password for the AIM account |
| `serverAddr` | OSCAR server address (IP:Port) |
| `profile` | Bot's profile text |
| `awayMessage` | Away message text |
| `useLocal` | `true` for local LLM, `false` for web API |
| `url` | LLM API endpoint |
| `model` | Model name (any string for KoboldCPP) |
| `minMs/maxMs` | Typing delay range in milliseconds |
| `perCharMs` | Additional delay per character |

### 3. Build and Run

```bash
# Build
go build -o aollm main.go

# Run
./aollm
```

You should see:
```
=== AOLLM v0.1 Starting ===
Config loaded: ScreenName=AOLLM, Server=localhost:5190
...
=== AOLLM is online! Waiting for messages... ===
```

### 4. Chat!

Send a message to the bot's screen name from any AIM client connected to your server!

## Building from Source

You'll need [Go 1.21+](https://go.dev/doc/install) installed.

### Install Dependencies

```bash
go mod download
```

### Build for Your OS

**Windows:**
```bash
go build -o aollm.exe main.go
```

**macOS:**
```bash
go build -o aollm main.go
```

**Linux:**
```bash
go build -o aollm main.go
```

### Cross-Compile for Other Platforms

Build for all platforms from any OS:

```bash
# Windows (64-bit)
GOOS=windows GOARCH=amd64 go build -o aollm-windows-amd64.exe main.go

# Windows (32-bit)
GOOS=windows GOARCH=386 go build -o aollm-windows-386.exe main.go

# macOS (Intel)
GOOS=darwin GOARCH=amd64 go build -o aollm-darwin-amd64 main.go

# macOS (Apple Silicon)
GOOS=darwin GOARCH=arm64 go build -o aollm-darwin-arm64 main.go

# Linux (64-bit)
GOOS=linux GOARCH=amd64 go build -o aollm-linux-amd64 main.go

# Linux (ARM/Raspberry Pi)
GOOS=linux GOARCH=arm GOARM=6 go build -o aollm-linux-arm main.go
```

### Build All at Once (PowerShell on Windows)

```powershell
$env:GOOS="windows"; $env:GOARCH="amd64"; go build -o builds/aollm-windows.exe main.go
$env:GOOS="darwin"; $env:GOARCH="arm64"; go build -o builds/aollm-macos-arm64 main.go
$env:GOOS="linux"; $env:GOARCH="amd64"; go build -o builds/aollm-linux main.go
```

## Using Web APIs (OpenAI, Groq, etc.)

To use a web API instead of a local LLM:

```json
"llm": {
  "useLocal": false,
  "web": {
    "apiKey": "your-api-key-here",
    "baseURL": "https://api.openai.com/v1",
    "model": "gpt-4o-mini"
  }
}
```

Popular options:
- **OpenAI**: `https://api.openai.com/v1` with `gpt-4o-mini`
- **Groq**: `https://api.groq.com/openai/v1` with `llama-3.1-8b-instant`
- **Together AI**: `https://api.together.xyz/v1` with various models

## Setting Up an AIM Server

This bot requires an OSCAR-compatible server. We recommend [open-oscar-server](https://github.com/mk6i/open-oscar-server):

1. Download the latest release
2. Run `open_oscar_server.exe`
3. Create accounts via the web interface (usually at http://localhost:8080)
4. Update `config.json` with your server's address

## Troubleshooting

**"LLM query failed: API returned status 404"**
- Make sure your LLM server is running
- Check the URL in config.json matches your LLM server's port

**"Connection error"**
- Verify the AIM server is running
- Check the serverAddr in config.json

**Bot shows online but doesn't respond**
- Check the console for LLM errors
- Verify your model is loaded in KoboldCPP/LM Studio

## The Persona

The bot is configured to act like a 2003 AIM user:
- Lowercase typing, lots of "lol", "brb", "g2g"
- Emoticons like `:P`, `:D`, `^_^`, `<3`
- References to Neopets, Winamp, dial-up, etc.
- Typos and casual grammar

You can customize the persona by editing the `systemPrompt` in `main.go` and recompiling.

## License

MIT License - Feel free to modify and distribute!

## Credits

- OSCAR protocol implementation based on [open-oscar-server](https://github.com/mk6i/open-oscar-server)
- Built with Go