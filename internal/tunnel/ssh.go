package tunnel

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// SSHTunnel создаёт TCP-туннель через SSH к удалённому серверу.
// Пробрасывает локальные порты на удалённые сервисы (Whisper, Ollama).
type SSHTunnel struct {
	host       string
	user       string
	port       int
	client     *ssh.Client
	mu         sync.Mutex
	forwarders []forwarder
	running    bool
}

type forwarder struct {
	localAddr  string
	remoteAddr string
	listener   net.Listener
}

// SSHConfig конфигурация SSH-подключения.
type SSHConfig struct {
	Host     string // e.g. "10.0.0.26"
	User     string // e.g. "dd"
	Port     int    // e.g. 22
	KeyPath  string // путь к приватному ключу (~/.ssh/id_rsa)
	Password string // пароль (альтернатива ключу)
}

func NewSSHTunnel(cfg SSHConfig) (*SSHTunnel, error) {
	if cfg.Port == 0 {
		cfg.Port = 22
	}

	// Строим SSH-конфиг
	sshConfig := &ssh.ClientConfig{
		User:            cfg.User,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // TODO: в проде проверять ключ
		Timeout:         10 * time.Second,
	}

	// Аутентификация: сначала ключ, потом пароль
	if cfg.KeyPath != "" {
		key, err := os.ReadFile(cfg.KeyPath)
		if err != nil {
			return nil, fmt.Errorf("ssh: read key %s: %w", cfg.KeyPath, err)
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			return nil, fmt.Errorf("ssh: parse key: %w", err)
		}
		sshConfig.Auth = append(sshConfig.Auth, ssh.PublicKeys(signer))
	}

	if cfg.Password != "" {
		sshConfig.Auth = append(sshConfig.Auth, ssh.Password(cfg.Password))
	}

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	log.Printf("[ssh] connecting to %s@%s ...", cfg.User, addr)

	client, err := ssh.Dial("tcp", addr, sshConfig)
	if err != nil {
		return nil, fmt.Errorf("ssh: dial %s: %w", addr, err)
	}

	log.Printf("[ssh] connected to %s", addr)

	return &SSHTunnel{
		host:   cfg.Host,
		user:   cfg.User,
		port:   cfg.Port,
		client: client,
	}, nil
}

// Forward создаёт локальный порт, который туннелируется на удалённый адрес.
// localAddr — адрес для прослушивания на локальной машине (e.g. "127.0.0.1:11434")
// remoteAddr — адрес удалённого сервиса (e.g. "127.0.0.1:11434")
func (t *SSHTunnel) Forward(localAddr, remoteAddr string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	listener, err := net.Listen("tcp", localAddr)
	if err != nil {
		return fmt.Errorf("ssh tunnel: listen %s: %w", localAddr, err)
	}

	f := forwarder{
		localAddr:  localAddr,
		remoteAddr: remoteAddr,
		listener:   listener,
	}
	t.forwarders = append(t.forwarders, f)
	t.running = true

	go t.acceptLoop(f)

	log.Printf("[ssh tunnel] %s → %s (via %s@%s)", localAddr, remoteAddr, t.user, t.host)
	return nil
}

func (t *SSHTunnel) acceptLoop(f forwarder) {
	for {
		conn, err := f.listener.Accept()
		if err != nil {
			// Listener закрыт
			return
		}
		go t.pipe(conn, f.remoteAddr)
	}
}

func (t *SSHTunnel) pipe(localConn net.Conn, remoteAddr string) {
	defer localConn.Close()

	remoteConn, err := t.client.Dial("tcp", remoteAddr)
	if err != nil {
		log.Printf("[ssh tunnel] dial remote %s: %v", remoteAddr, err)
		return
	}
	defer remoteConn.Close()

	// Двунаправленное копирование данных
	done := make(chan struct{}, 2)

	go func() {
		io.Copy(remoteConn, localConn)
		done <- struct{}{}
	}()
	go func() {
		io.Copy(localConn, remoteConn)
		done <- struct{}{}
	}()

	<-done
}

// Close закрывает все туннели и SSH-соединение.
func (t *SSHTunnel) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()

	for _, f := range t.forwarders {
		f.listener.Close()
		log.Printf("[ssh tunnel] closed %s", f.localAddr)
	}

	if t.client != nil {
		t.client.Close()
		log.Printf("[ssh tunnel] disconnected from %s@%s", t.user, t.host)
	}

	t.running = false
}

// IsConnected проверяет, живо ли SSH-соединение.
func (t *SSHTunnel) IsConnected() bool {
	if t.client == nil {
		return false
	}
	_, _, err := t.client.SendRequest("keepalive@openssh.com", true, nil)
	return err == nil
}
