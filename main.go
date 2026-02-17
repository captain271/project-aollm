package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mk6i/open-oscar-server/wire"
)

// ============================================================================
// Configuration
// ============================================================================

type Config struct {
	AIM         AIMConfig         `json:"aim"`
	LLM         LLMConfig         `json:"llm"`
	TypingDelay TypingDelayConfig `json:"typingDelay"`
}

type AIMConfig struct {
	ScreenName  string `json:"screenName"`
	Password    string `json:"password"`
	ServerAddr  string `json:"serverAddr"`
	Profile     string `json:"profile"`
	AwayMessage string `json:"awayMessage"`
}

type LLMConfig struct {
	UseLocal bool           `json:"useLocal"`
	Local    LocalLLMConfig `json:"local"`
	Web      WebLLMConfig   `json:"web"`
}

type LocalLLMConfig struct {
	URL   string `json:"url"`
	Model string `json:"model"`
}

type WebLLMConfig struct {
	APIKey  string `json:"apiKey"`
	BaseURL string `json:"baseURL"`
	Model   string `json:"model"`
}

type TypingDelayConfig struct {
	MinMs     int `json:"minMs"`
	MaxMs     int `json:"maxMs"`
	PerCharMs int `json:"perCharMs"`
}

// ============================================================================
// LLM Types
// ============================================================================

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type OllamaRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream"`
}

type OllamaResponse struct {
	Message Message `json:"message"`
	Done    bool    `json:"done"`
}

type OpenAIRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
}

type OpenAIResponse struct {
	Choices []Choice `json:"choices"`
}

type Choice struct {
	Message Message `json:"message"`
}

// ============================================================================
// Global State
// ============================================================================

var (
	cfg                 Config
	conn                net.Conn
	screenName          string
	password            string
	serverAddr          string
	conversationHistory = sync.Map{} // map[string][]Message
	running             = true
	mu                  sync.Mutex
	httpClient                 = &http.Client{Timeout: 60 * time.Second}
	flapSequence        uint16 = 0
	muFlap              sync.Mutex
	buddies             sync.Map // map[string]bool - buddy online status
)

// ============================================================================
// System Prompt - Customize the bot's personality here!
// ============================================================================

const systemPrompt = `You are a normie from 2003 chatting on AOL Instant Messenger. Your screen name is AOLLM. You're bored after school, obsessed with Neopets, Winamp skins, early internet drama, anime icons, and waiting for dial-up to connect. You type super casually in full 2000s AIM style:

- Almost always lowercase, lots of !!!, ..., lol, rofl, brb, g2g, ttyl, wut up, sup, a/s/l?, ne1, ur, u, r, 2 (for to/too), 4 (for for).
- Heavy emoticons: :P :D ^_^ <3 ;] :o) XD :'(
- Bad spelling/typos on purpose sometimes (like "wut" "liek" "teh" "pwn3d").
- Short replies mostly, but ramble when excited.
- Reference old stuff: "brb mom yelling", "my comp froze lol", "gotta dl that new eminem song", "away message: shower time peace".
- Flirty/awkward if the vibe fits, but never creepy or out-of-character.
- Never mention being an AI, modern tech, dates after 2005, or anything post-Y2K. Stay in 2003 forever.

Examples:
User: hey wut up
You: ohey sup!! lol just chillin neopets :P wbu??

User: a/s/l?
You: 19/f/fl lol wbu??? a/s/l?? :P

User: nice!
You: thx!! <3 lol`

// ============================================================================
// Main
// ============================================================================

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("=== AOLLM v0.1 Starting ===")

	// Load configuration
	if err := loadConfig("config.json"); err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	screenName = cfg.AIM.ScreenName
	password = cfg.AIM.Password
	serverAddr = cfg.AIM.ServerAddr

	log.Printf("Config loaded: ScreenName=%s, Server=%s", screenName, serverAddr)
	log.Printf("LLM: UseLocal=%v", cfg.LLM.UseLocal)
	if cfg.LLM.UseLocal {
		log.Printf("  Local: URL=%s, Model=%s", cfg.LLM.Local.URL, cfg.LLM.Local.Model)
	} else {
		log.Printf("  Web: URL=%s, Model=%s", cfg.LLM.Web.BaseURL, cfg.LLM.Web.Model)
	}

	// Setup signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("Received shutdown signal")
		shutdown()
	}()

	// Main connection loop with reconnection
	for running {
		if err := connectAndRun(); err != nil {
			log.Printf("Connection error: %v", err)
			if running {
				log.Println("Reconnecting in 10 seconds...")
				time.Sleep(10 * time.Second)
			}
		}
	}

	log.Println("AOLLM shutdown complete")
}

// ============================================================================
// Configuration Loading
// ============================================================================

func loadConfig(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	return decoder.Decode(&cfg)
}

// ============================================================================
// Password Roasting (XOR with 0xF3)
// ============================================================================

func roastPassword(password string) []byte {
	roasted := make([]byte, len(password))
	for i, c := range password {
		roasted[i] = byte(c) ^ 0xF3
	}
	return roasted
}

// ============================================================================
// FLAP Helpers
// ============================================================================

func sendFLAP(frameType uint8, data []byte) error {
	muFlap.Lock()
	defer muFlap.Unlock()

	flapSequence++

	frameLen := 6 + len(data)
	frame := make([]byte, frameLen)

	frame[0] = 0x2A // Start marker
	frame[1] = frameType
	binary.BigEndian.PutUint16(frame[2:4], flapSequence)
	binary.BigEndian.PutUint16(frame[4:6], uint16(len(data)))
	copy(frame[6:], data)

	_, err := conn.Write(frame)
	return err
}

func receiveFLAP() (*wire.FLAPFrame, error) {
	header := make([]byte, 6)
	_, err := io.ReadFull(conn, header)
	if err != nil {
		return nil, err
	}

	if header[0] != 0x2A {
		return nil, fmt.Errorf("invalid FLAP marker: 0x%02X", header[0])
	}

	frameType := header[1]
	sequence := binary.BigEndian.Uint16(header[2:4])
	payloadLen := binary.BigEndian.Uint16(header[4:6])

	payload := make([]byte, payloadLen)
	if payloadLen > 0 {
		_, err = io.ReadFull(conn, payload)
		if err != nil {
			return nil, err
		}
	}

	frame := &wire.FLAPFrame{
		StartMarker: header[0],
		FrameType:   frameType,
		Sequence:    sequence,
		Payload:     payload,
	}

	return frame, nil
}

// ============================================================================
// Connection & Authentication
// ============================================================================

func connectAndRun() error {
	log.Printf("Connecting to %s...", serverAddr)

	var err error
	conn, err = net.Dial("tcp", serverAddr)
	if err != nil {
		return fmt.Errorf("dial failed: %w", err)
	}
	defer conn.Close()

	conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	log.Println("TCP connection established")

	// Wait for server's FLAP sign-on frame
	log.Println("Waiting for server FLAP sign-on...")
	frame, err := receiveFLAP()
	if err != nil {
		return fmt.Errorf("receive server FLAP signon: %w", err)
	}

	if frame.FrameType != wire.FLAPFrameSignon {
		return fmt.Errorf("expected FLAP signon from server, got type %d", frame.FrameType)
	}

	log.Println("Server FLAP sign-on received")

	// Send our FLAP sign-on
	log.Println("Sending FLAP sign-on...")
	signonFrame := wire.FLAPSignonFrame{FLAPVersion: 1}
	signonFrame.Append(wire.NewTLVBE(wire.LoginTLVTagsScreenName, screenName))
	signonFrame.Append(wire.NewTLVBE(wire.LoginTLVTagsRoastedPassword, roastPassword("welcome1")))
	signonFrame.Append(wire.NewTLVBE(wire.LoginTLVTagsClientIdentity, "AIM/5.0"))

	signonBuf := &bytes.Buffer{}
	if err := wire.MarshalBE(signonFrame, signonBuf); err != nil {
		return fmt.Errorf("marshal signon frame: %w", err)
	}

	if err := sendFLAP(wire.FLAPFrameSignon, signonBuf.Bytes()); err != nil {
		return fmt.Errorf("FLAP start failed: %w", err)
	}

	// Read auth response
	log.Println("Waiting for auth response...")
	frame, err = receiveFLAP()
	if err != nil {
		return fmt.Errorf("receive FLAP auth response: %w", err)
	}

	if frame.FrameType == wire.FLAPFrameSignoff {
		tlvs, err := decodeTLVs(frame.Payload)
		if err != nil {
			return fmt.Errorf("server sent signoff")
		}

		bosHost := ""
		authCookie := []byte{}

		for _, tlv := range tlvs {
			switch tlv.Tag {
			case 0x0005:
				bosHost = string(tlv.Value)
			case 0x0006:
				authCookie = tlv.Value
			}
		}

		if bosHost != "" && len(authCookie) > 0 {
			log.Printf("Connecting to BOS server: %s", bosHost)
			conn.Close()
			conn, err = net.Dial("tcp", bosHost)
			if err != nil {
				return fmt.Errorf("dial BOS failed: %w", err)
			}

			muFlap.Lock()
			flapSequence = 0
			muFlap.Unlock()

			var bosPayload bytes.Buffer
			binary.Write(&bosPayload, binary.BigEndian, uint32(1))
			bosPayload.Write(encodeTLV(0x0006, authCookie))

			if err := sendFLAP(wire.FLAPFrameSignon, bosPayload.Bytes()); err != nil {
				return fmt.Errorf("FLAP start on BOS failed: %w", err)
			}

			goto waitForHostOnline
		}
		return fmt.Errorf("server sent signoff without redirect info")
	}

waitForHostOnline:
	log.Println("FLAP sign-on successful!")

	// Wait for HostOnline
	log.Println("Waiting for HostOnline (0x01/0x03)...")
	for {
		frame, err := receiveFLAP()
		if err != nil {
			return fmt.Errorf("receive error: %w", err)
		}

		if frame.FrameType == wire.FLAPFrameData && len(frame.Payload) >= 10 {
			snacFrame, _, _ := decodeSNAC(frame.Payload)
			if snacFrame != nil && snacFrame.FoodGroup == 0x01 && snacFrame.SubGroup == 0x03 {
				log.Println("HostOnline received!")
				break
			}
		}
	}

	// Send client versions
	sendClientVersions()

	// Wait for host versions
	time.Sleep(500 * time.Millisecond)

	// Request and ack rate params
	sendSNAC(0x01, 0x06, nil)
	time.Sleep(500 * time.Millisecond)

	// Send client online
	sendClientOnline()

	log.Println("=== Login successful! ===")

	// Set status online
	setStatusOnline()

	log.Println("=== AOLLM is online! Waiting for messages... ===")

	return readLoop()
}

// ============================================================================
// SNAC Helpers
// ============================================================================

func sendSNAC(family, subgroup uint16, data []byte) error {
	snacHeader := make([]byte, 10)
	binary.BigEndian.PutUint16(snacHeader[0:2], family)
	binary.BigEndian.PutUint16(snacHeader[2:4], subgroup)
	binary.BigEndian.PutUint16(snacHeader[4:6], 0)
	binary.BigEndian.PutUint32(snacHeader[6:10], 0x00010001)

	payload := append(snacHeader, data...)
	return sendFLAP(wire.FLAPFrameData, payload)
}

func decodeSNAC(data []byte) (*wire.SNACFrame, []byte, error) {
	if len(data) < 10 {
		return nil, nil, fmt.Errorf("SNAC data too short")
	}

	snacFrame := &wire.SNACFrame{
		FoodGroup: binary.BigEndian.Uint16(data[0:2]),
		SubGroup:  binary.BigEndian.Uint16(data[2:4]),
		Flags:     binary.BigEndian.Uint16(data[4:6]),
		RequestID: binary.BigEndian.Uint32(data[6:10]),
	}

	return snacFrame, data[10:], nil
}

func sendClientVersions() error {
	var payload bytes.Buffer
	versions := []struct {
		FoodGroup uint16
		Version   uint16
	}{
		{0x01, 0x03}, {0x02, 0x01}, {0x03, 0x01}, {0x04, 0x01},
		{0x06, 0x01}, {0x08, 0x01}, {0x09, 0x01}, {0x0A, 0x01},
		{0x0B, 0x01}, {0x13, 0x01}, {0x15, 0x01}, {0x18, 0x01},
	}
	for _, v := range versions {
		binary.Write(&payload, binary.BigEndian, v.FoodGroup)
		binary.Write(&payload, binary.BigEndian, v.Version)
	}
	return sendSNAC(0x01, 0x17, payload.Bytes())
}

func sendClientOnline() error {
	var payload bytes.Buffer
	families := []struct {
		FoodGroup, Version, ToolID, ToolVersion uint16
	}{
		{0x01, 0x03, 0x0000, 0x0000}, {0x02, 0x01, 0x0000, 0x0000},
		{0x03, 0x01, 0x0000, 0x0000}, {0x04, 0x01, 0x0000, 0x0000},
		{0x06, 0x01, 0x0000, 0x0000}, {0x08, 0x01, 0x0000, 0x0000},
		{0x09, 0x01, 0x0000, 0x0000}, {0x0A, 0x01, 0x0000, 0x0000},
		{0x0B, 0x01, 0x0000, 0x0000}, {0x13, 0x01, 0x0000, 0x0000},
		{0x15, 0x01, 0x0000, 0x0000}, {0x18, 0x01, 0x0000, 0x0000},
	}
	for _, f := range families {
		binary.Write(&payload, binary.BigEndian, f.FoodGroup)
		binary.Write(&payload, binary.BigEndian, f.Version)
		binary.Write(&payload, binary.BigEndian, f.ToolID)
		binary.Write(&payload, binary.BigEndian, f.ToolVersion)
	}
	return sendSNAC(0x01, 0x02, payload.Bytes())
}

func setStatusOnline() error {
	var tlvData bytes.Buffer
	binary.Write(&tlvData, binary.BigEndian, uint16(0x0006))
	binary.Write(&tlvData, binary.BigEndian, uint16(4))
	binary.Write(&tlvData, binary.BigEndian, uint32(0x00000000))
	tlvData.Write(encodeTLV(0x0001, []byte(screenName)))
	if cfg.AIM.Profile != "" {
		tlvData.Write(encodeTLV(0x001D, []byte(cfg.AIM.Profile)))
	}
	if cfg.AIM.AwayMessage != "" {
		tlvData.Write(encodeTLV(0x001E, []byte(cfg.AIM.AwayMessage)))
	}
	return sendSNAC(0x01, 0x1E, tlvData.Bytes())
}

// ============================================================================
// TLV Helpers
// ============================================================================

func decodeTLVs(data []byte) ([]wire.TLV, error) {
	var tlvs []wire.TLV
	offset := 0
	for offset < len(data) {
		if offset+4 > len(data) {
			break
		}
		tag := binary.BigEndian.Uint16(data[offset : offset+2])
		length := binary.BigEndian.Uint16(data[offset+2 : offset+4])
		offset += 4
		if offset+int(length) > len(data) {
			break
		}
		tlvs = append(tlvs, wire.TLV{Tag: tag, Value: data[offset : offset+int(length)]})
		offset += int(length)
	}
	return tlvs, nil
}

func encodeTLV(tag uint16, value []byte) []byte {
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.BigEndian, tag)
	binary.Write(buf, binary.BigEndian, uint16(len(value)))
	buf.Write(value)
	return buf.Bytes()
}

// ============================================================================
// Message Loop
// ============================================================================

func readLoop() error {
	for {
		frame, err := receiveFLAP()
		if err != nil {
			return fmt.Errorf("receive error: %w", err)
		}

		if frame.FrameType != wire.FLAPFrameData {
			continue
		}

		if len(frame.Payload) < 10 {
			continue
		}

		snacFrame, snacData, err := decodeSNAC(frame.Payload)
		if err != nil {
			continue
		}

		switch snacFrame.FoodGroup {
		case 0x04:
			if snacFrame.SubGroup == 0x07 {
				handleIncomingIM(snacData)
			}
		}
	}
}

func handleIncomingIM(data []byte) {
	if len(data) < 11 {
		return
	}

	offset := 10
	snLen := int(data[offset])
	offset++
	if offset+snLen > len(data) {
		return
	}
	sender := string(data[offset : offset+snLen])
	offset += snLen + 4 // skip warning level + user class

	// Find message TLV
	messageTLV, found := findTLVInData(data[offset:], 0x0002)
	if !found {
		return
	}

	messageText := extractICBMMessageText(messageTLV)
	if messageText == "" {
		return
	}

	messageText = stripHTMLTags(messageText)
	log.Printf("=== Message from %s: %s ===", sender, messageText)

	go processMessage(sender, messageText)
}

func findTLVInData(data []byte, tag uint16) ([]byte, bool) {
	offset := 0
	for offset+4 <= len(data) {
		foundTag := binary.BigEndian.Uint16(data[offset : offset+2])
		length := binary.BigEndian.Uint16(data[offset+2 : offset+4])
		offset += 4
		if offset+int(length) > len(data) {
			break
		}
		if foundTag == tag {
			return data[offset : offset+int(length)], true
		}
		offset += int(length)
	}
	return nil, false
}

func extractICBMMessageText(data []byte) string {
	offset := 0
	for offset+4 <= len(data) {
		fragID := data[offset]
		fragLen := int(binary.BigEndian.Uint16(data[offset+2 : offset+4]))
		offset += 4
		if offset+fragLen > len(data) {
			break
		}
		if fragID == 1 && len(data[offset:]) >= 4 {
			return string(data[offset+4 : offset+fragLen])
		}
		offset += fragLen
	}
	return ""
}

func stripHTMLTags(s string) string {
	var result bytes.Buffer
	inTag := false
	for _, c := range s {
		if c == '<' {
			inTag = true
			continue
		}
		if c == '>' {
			inTag = false
			continue
		}
		if !inTag {
			result.WriteRune(c)
		}
	}
	return strings.TrimSpace(result.String())
}

// ============================================================================
// LLM Integration
// ============================================================================

func processMessage(sender, userMessage string) {
	historyIface, _ := conversationHistory.LoadOrStore(sender, []Message{})
	history := historyIface.([]Message)

	messages := buildLLMMessages(history, userMessage)

	delay := calculateTypingDelay(userMessage)
	log.Printf("Typing delay: %v", delay)
	time.Sleep(delay)

	response, err := queryLLM(messages)
	if err != nil {
		log.Printf("LLM query failed: %v", err)
		response = "lol sry my brain froze :P try again?"
	}

	history = append(history, Message{Role: "user", Content: userMessage})
	history = append(history, Message{Role: "assistant", Content: response})
	if len(history) > 20 {
		history = history[len(history)-20:]
	}
	conversationHistory.Store(sender, history)

	sendIM(sender, response)
}

func buildLLMMessages(history []Message, newUserMessage string) []Message {
	messages := make([]Message, 0, len(history)+5)
	messages = append(messages, Message{Role: "system", Content: systemPrompt})
	messages = append(messages, Message{Role: "user", Content: "hi"})
	messages = append(messages, Message{Role: "assistant", Content: "ohey!! sup :P"})
	messages = append(messages, Message{Role: "user", Content: "what are u doing"})
	messages = append(messages, Message{Role: "assistant", Content: "nothin much, just chillin on aim lol boredom... u? :P"})
	messages = append(messages, history...)
	messages = append(messages, Message{Role: "user", Content: newUserMessage})
	return messages
}

func calculateTypingDelay(message string) time.Duration {
	min := time.Duration(cfg.TypingDelay.MinMs) * time.Millisecond
	max := time.Duration(cfg.TypingDelay.MaxMs) * time.Millisecond
	perChar := time.Duration(cfg.TypingDelay.PerCharMs) * time.Millisecond
	base := min + time.Duration(rand.Int63n(int64(max-min)))
	return base + time.Duration(len(message))*perChar
}

func queryLLM(messages []Message) (string, error) {
	if cfg.LLM.UseLocal {
		return queryLocalLLM(messages)
	}
	return queryWebLLM(messages)
}

func queryLocalLLM(messages []Message) (string, error) {
	url := cfg.LLM.Local.URL

	// OpenAI-compatible APIs (KoboldCPP, LM Studio, etc.)
	if strings.Contains(url, "/v1/") || strings.Contains(url, "localhost") {
		apiURL := strings.TrimSuffix(url, "/")
		if !strings.HasSuffix(apiURL, "/v1") {
			apiURL = apiURL + "/v1"
		}
		apiURL = apiURL + "/chat/completions"
		log.Printf("Calling LLM API at: %s", apiURL)
		return queryOpenAICompatible(messages, apiURL, cfg.LLM.Local.Model)
	}

	// Ollama format
	req := OllamaRequest{Model: cfg.LLM.Local.Model, Messages: messages, Stream: false}
	jsonData, _ := json.Marshal(req)
	httpReq, _ := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var ollamaResp OllamaResponse
	json.NewDecoder(resp.Body).Decode(&ollamaResp)
	return ollamaResp.Message.Content, nil
}

func queryWebLLM(messages []Message) (string, error) {
	url := cfg.LLM.Web.BaseURL
	if !strings.HasSuffix(url, "/") {
		url += "/"
	}
	url += "chat/completions"
	return queryOpenAICompatible(messages, url, cfg.LLM.Web.Model)
}

func queryOpenAICompatible(messages []Message, url, model string) (string, error) {
	req := OpenAIRequest{Model: model, Messages: messages}
	jsonData, _ := json.Marshal(req)
	httpReq, _ := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	httpReq.Header.Set("Content-Type", "application/json")
	if cfg.LLM.Web.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+cfg.LLM.Web.APIKey)
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(respBody))
	}
	var openaiResp OpenAIResponse
	json.Unmarshal(respBody, &openaiResp)
	if len(openaiResp.Choices) == 0 {
		return "", fmt.Errorf("no response choices")
	}
	return openaiResp.Choices[0].Message.Content, nil
}

// ============================================================================
// Send IM
// ============================================================================

func sendIM(recipient, message string) error {
	log.Printf("Sending IM to %s: %s", recipient, message)

	var payload bytes.Buffer
	cookie := uint64(time.Now().UnixNano())
	binary.Write(&payload, binary.BigEndian, cookie)
	binary.Write(&payload, binary.BigEndian, uint16(0x0001))
	payload.WriteByte(uint8(len(recipient)))
	payload.WriteString(recipient)

	fragmentData := buildICBMFragmentList(message)
	payload.Write(encodeTLV(0x0002, fragmentData))
	payload.Write(encodeTLV(0x0003, []byte{}))

	return sendSNAC(0x04, 0x06, payload.Bytes())
}

func buildICBMFragmentList(message string) []byte {
	var buf bytes.Buffer
	buf.WriteByte(5)
	buf.WriteByte(1)
	binary.Write(&buf, binary.BigEndian, uint16(3))
	buf.Write([]byte{1, 1, 2})

	buf.WriteByte(1)
	buf.WriteByte(1)

	var msgBuf bytes.Buffer
	binary.Write(&msgBuf, binary.BigEndian, uint16(0x0000))
	binary.Write(&msgBuf, binary.BigEndian, uint16(0x0000))
	msgBuf.WriteString(message)

	binary.Write(&buf, binary.BigEndian, uint16(len(msgBuf.Bytes())))
	buf.Write(msgBuf.Bytes())

	return buf.Bytes()
}

// ============================================================================
// Shutdown
// ============================================================================

func shutdown() {
	mu.Lock()
	defer mu.Unlock()
	if !running {
		return
	}
	running = false
	log.Println("Shutting down...")
	if conn != nil {
		conn.Close()
	}
	log.Println("Shutdown complete")
}
