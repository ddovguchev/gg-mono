package audio

import (
	"log"
	"sync"

	"github.com/gordonklaus/portaudio"
)

const (
	captureSampleRate = 16000 // Whisper требует 16kHz
	captureChannels   = 1     // Mono
	captureFrames     = 1024  // Размер буфера
)

// Capturer захватывает аудио с микрофона в реальном времени.
type Capturer struct {
	stream    *portaudio.Stream
	buf       []float32 // буфер, переданный в OpenStream
	ch        chan []float32
	deviceName string
	mu        sync.Mutex
	running   bool
}

// DeviceInfo информация об аудиоустройстве.
type DeviceInfo struct {
	Name       string
	MaxInputs  int
	MaxOutputs int
}

// ListDevices возвращает список доступных аудиоустройств.
func ListDevices() ([]DeviceInfo, error) {
	if err := portaudio.Initialize(); err != nil {
		return nil, err
	}

	devices, err := portaudio.Devices()
	if err != nil {
		return nil, err
	}

	var result []DeviceInfo
	for _, d := range devices {
		result = append(result, DeviceInfo{
			Name:       d.Name,
			MaxInputs:  d.MaxInputChannels,
			MaxOutputs: d.MaxOutputChannels,
		})
	}

	return result, nil
}

// NewCapturer создаёт захватчик аудио с указанного устройства.
// deviceName — имя устройства (пустая строка = default).
func NewCapturer(deviceName string) (*Capturer, error) {
	if err := portaudio.Initialize(); err != nil {
		return nil, err
	}

	// Выбираем устройство
	var inputDevice *portaudio.DeviceInfo
	if deviceName != "" && deviceName != "default" {
		devices, err := portaudio.Devices()
		if err != nil {
			return nil, err
		}
		for _, d := range devices {
			if d.Name == deviceName && d.MaxInputChannels > 0 {
				inputDevice = d
				break
			}
		}
	}

	if inputDevice == nil {
		var err error
		inputDevice, err = portaudio.DefaultInputDevice()
		if err != nil {
			// Fallback: если дефолтного входа нет — берём любой доступный.
			log.Printf("[audio] no default input (%v), searching fallback...", err)
			devices, derr := portaudio.Devices()
			if derr != nil {
				return nil, err
			}
			for _, d := range devices {
				if d.MaxInputChannels > 0 {
					inputDevice = d
					log.Printf("[audio] fallback input: %s", d.Name)
					break
				}
			}
			if inputDevice == nil {
				return nil, err
			}
		}
	}

	log.Printf("[audio] capture device: %s (max inputs: %d)", inputDevice.Name, inputDevice.MaxInputChannels)

	// Параметры потока
	params := portaudio.LowLatencyParameters(inputDevice, nil)
	params.Input.Device = inputDevice
	params.Input.Channels = captureChannels
	params.SampleRate = captureSampleRate
	params.FramesPerBuffer = captureFrames

	buf := make([]float32, captureFrames)

	stream, err := portaudio.OpenStream(params, buf)
	if err != nil {
		return nil, err
	}

	return &Capturer{
		stream:     stream,
		buf:        buf,
		ch:         make(chan []float32, 100),
		deviceName: inputDevice.Name,
	}, nil
}

// Start начинает захват аудио. Чтение — через Channel().
func (c *Capturer) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.running {
		return nil
	}

	if err := c.stream.Start(); err != nil {
		return err
	}

	c.running = true

	// Горутина чтения аудиоданных
	go c.readLoop()

	log.Printf("[audio] capture started (16kHz mono)")
	return nil
}

func (c *Capturer) readLoop() {
	for {
		if err := c.stream.Read(); err != nil {
			log.Printf("[audio] capture error: %v", err)
			return
		}
		// Копируем данные из буфера, чтобы не перезаписались при следующем Read
		chunk := make([]float32, len(c.buf))
		copy(chunk, c.buf)

		// Неблокирующая отправка
		select {
		case c.ch <- chunk:
		default:
			// Буфер переполнен — пропускаем чанк
		}
	}
}

// Stop останавливает захват.
func (c *Capturer) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.running {
		return
	}

	c.stream.Stop()
	c.running = false
	log.Printf("[audio] capture stopped")
}

// Close освобождает ресурсы.
func (c *Capturer) Close() {
	c.Stop()
	c.stream.Close()
}

// Channel возвращает канал с аудиочанками (float32, 16kHz mono).
func (c *Capturer) Channel() <-chan []float32 {
	return c.ch
}

// SampleRate возвращает частоту дискретизации.
func (c *Capturer) SampleRate() float64 {
	return captureSampleRate
}
