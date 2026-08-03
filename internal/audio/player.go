package audio

import (
	"encoding/binary"
	"fmt"
	"log"
	"sync"
	"sync/atomic"

	"github.com/gordonklaus/portaudio"
)

const (
	playerSampleRate = 24000 // Fish Audio typically outputs 24kHz
	playerChannels   = 1
	playerFrames     = 1024
)

// Player воспроизводит аудио через виртуальный микрофон (BlackHole / Loopback).
type Player struct {
	stream     *portaudio.Stream
	buf        []float32 // буфер, переданный в OpenStream
	deviceName string
	mu         sync.Mutex
	running    bool
	// playing — идёт ли сейчас воспроизведение перевода.
	playing atomic.Bool
	// aec — компенсатор эха: плеер сообщает, что именно играет, чтобы
	// пайплайн мог вычесть эхо из микрофонного сигнала.
	aec *EchoCanceller
}

// AEC возвращает компенсатор эха плеера (для пайплайна).
func (p *Player) AEC() *EchoCanceller {
	return p.aec
}

// NewPlayer создаёт плеер для вывода аудио на указанное устройство.
// deviceName — имя устройства (пустая строка = default output).
func NewPlayer(deviceName string) (*Player, error) {
	if err := portaudio.Initialize(); err != nil {
		return nil, err
	}

	// Выбираем устройство
	var outputDevice *portaudio.DeviceInfo
	if deviceName != "" && deviceName != "default" {
		devices, err := portaudio.Devices()
		if err != nil {
			return nil, err
		}
		for _, d := range devices {
			if d.Name == deviceName && d.MaxOutputChannels > 0 {
				outputDevice = d
				break
			}
		}
	}

	if outputDevice == nil {
		var err error
		outputDevice, err = portaudio.DefaultOutputDevice()
		if err != nil {
			// Fallback: если дефолтного выхода нет (например, временный
			// сбой CoreAudio) — берём любой доступный выход.
			log.Printf("[audio] no default output (%v), searching fallback...", err)
			devices, derr := portaudio.Devices()
			if derr != nil {
				return nil, err
			}
			for _, d := range devices {
				if d.MaxOutputChannels > 0 {
					outputDevice = d
					log.Printf("[audio] fallback output: %s", d.Name)
					break
				}
			}
			if outputDevice == nil {
				return nil, err
			}
		}
	}

	log.Printf("[audio] player device: %s", outputDevice.Name)

	params := portaudio.LowLatencyParameters(nil, outputDevice)
	params.Output.Device = outputDevice
	params.Output.Channels = playerChannels
	params.SampleRate = playerSampleRate
	params.FramesPerBuffer = playerFrames

	buf := make([]float32, playerFrames)

	stream, err := portaudio.OpenStream(params, buf)
	if err != nil {
		return nil, err
	}

	return &Player{
		stream:     stream,
		buf:        buf,
		deviceName: outputDevice.Name,
		aec:        NewEchoCanceller(),
	}, nil
}

// Start запускает воспроизведение.
func (p *Player) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.running {
		return nil
	}

	if err := p.stream.Start(); err != nil {
		return err
	}

	p.running = true

	// Prime: сразу наполняем выходной буфер тишиной, чтобы первый реальный
	// звук не ловил underflow (устройство не должно голодать до его прихода).
	for i := 0; i < 4; i++ {
		for j := range p.buf {
			p.buf[j] = 0
		}
		if err := p.stream.Write(); err != nil {
			log.Printf("[audio] player prime write: %v", err)
			break
		}
	}

	log.Printf("[audio] player started")
	return nil
}

// writeSamples записывает float32 samples в буфер и отправляет на устройство.
// Обрабатывает данные чанками по playerFrames.
func (p *Player) writeSamples(samples []float32) error {
	for i := 0; i < len(samples); i += playerFrames {
		end := i + playerFrames
		if end > len(samples) {
			end = len(samples)
		}

		// Копируем данные в буфер (с padding если нужно)
		n := copy(p.buf, samples[i:end])

		// Дополняем нулями если чанк меньше буфера
		for j := n; j < len(p.buf); j++ {
			p.buf[j] = 0
		}

		if err := p.stream.Write(); err != nil {
			// OutputUnderflowed — это предупреждение о том, что выходной буфер
			// голодал в прошлом (долгая пауза между фразами). Данные при этом
			// записываются и доигрываются, поэтому прерывать воспроизведение
			// нельзя — иначе перевод вообще не звучал бы.
			if err == portaudio.OutputUnderflowed {
				log.Printf("[audio] player underflow (ignored, non-fatal)")
				continue
			}
			return err
		}

		// Подаём сыгранное в компенсатор эха (референс на 16 кГц — как капча).
		if p.aec != nil {
			p.aec.AddReference(ResampleFloat32(p.buf[:n], playerSampleRate, 16000))
		}
	}
	return nil
}

// PlayChunk воспроизводит float32 samples.
func (p *Player) PlayChunk(samples []float32) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.running {
		return fmt.Errorf("player not running")
	}

	p.playing.Store(true)
	defer p.playing.Store(false)
	return p.writeSamples(samples)
}

// IsPlaying — идёт ли сейчас воспроизведение аудио.
func (p *Player) IsPlaying() bool {
	return p.playing.Load()
}

// PlayWAV декодирует WAV (16-bit PCM, mono) и воспроизводит его.
func (p *Player) PlayWAV(data []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.running {
		return fmt.Errorf("player not running")
	}

	p.playing.Store(true)
	defer p.playing.Store(false)

	// Конвертируем int16 PCM → float32
	samples := make([]float32, len(data)/2)
	for i := 0; i < len(data)-1; i += 2 {
		sample := int16(binary.LittleEndian.Uint16(data[i : i+2]))
		samples[i/2] = float32(sample) / 32768.0
	}

	return p.writeSamples(samples)
}

// PlayMP3 декодирует MP3 (заглушка).
func (p *Player) PlayMP3(data []byte) error {
	log.Printf("[audio] MP3 playback not implemented yet (%d bytes received)", len(data))
	return nil
}

// Stop останавливает воспроизведение.
func (p *Player) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.running {
		return
	}

	p.stream.Stop()
	p.running = false
	log.Printf("[audio] player stopped")
}

// Close освобождает ресурсы.
func (p *Player) Close() {
	p.Stop()
	p.stream.Close()
}
