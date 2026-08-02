package audio

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
)

// Float32ToWAV конвертирует float32 mono samples в WAV-файл (16-bit PCM, 16kHz).
func Float32ToWAV(samples []float32, sampleRate int) []byte {
	// Конвертируем float32 → int16
	pcm := make([]byte, len(samples)*2)
	for i, s := range samples {
		// Clamp to [-1, 1]
		if s > 1 {
			s = 1
		} else if s < -1 {
			s = -1
		}
		v := int16(s * 32767)
		binary.LittleEndian.PutUint16(pcm[i*2:i*2+2], uint16(v))
	}

	return buildWAV(pcm, sampleRate, 1, 16)
}

// buildWAV создаёт WAV-файл из сырых PCM-данных.
func buildWAV(pcmData []byte, sampleRate, channels, bitsPerSample int) []byte {
	var buf bytes.Buffer

	dataSize := len(pcmData)
	channelsInt := channels
	blockAlign := channelsInt * bitsPerSample / 8
	byteRate := sampleRate * blockAlign

	// RIFF header
	buf.WriteString("RIFF")
	writeUint32(&buf, uint32(36+dataSize))
	buf.WriteString("WAVE")

	// fmt chunk
	buf.WriteString("fmt ")
	writeUint32(&buf, 16)                       // chunk size
	writeUint16(&buf, 1)                        // PCM format
	writeUint16(&buf, uint16(channelsInt))
	writeUint32(&buf, uint32(sampleRate))
	writeUint32(&buf, uint32(byteRate))
	writeUint16(&buf, uint16(blockAlign))
	writeUint16(&buf, uint16(bitsPerSample))

	// data chunk
	buf.WriteString("data")
	writeUint32(&buf, uint32(dataSize))
	buf.Write(pcmData)

	return buf.Bytes()
}

func writeUint32(buf *bytes.Buffer, v uint32) {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, v)
	buf.Write(b)
}

func writeUint16(buf *bytes.Buffer, v uint16) {
	b := make([]byte, 2)
	binary.LittleEndian.PutUint16(b, v)
	buf.Write(b)
}

// ConcatenateFloat32 соединяет несколько float32-буферов в один.
func ConcatenateFloat32(chunks [][]float32) []float32 {
	total := 0
	for _, c := range chunks {
		total += len(c)
	}
	result := make([]float32, 0, total)
	for _, c := range chunks {
		result = append(result, c...)
	}
	return result
}

// ResampleFloat32 ресемплирует float32 audio с одной частоты на другую.
// Использует простой линейный интерполяции.
func ResampleFloat32(samples []float32, fromRate, toRate int) []float32 {
	if fromRate == toRate {
		return samples
	}

	ratio := float64(toRate) / float64(fromRate)
	newLen := int(math.Round(float64(len(samples)) * ratio))
	result := make([]float32, newLen)

	for i := 0; i < newLen; i++ {
		srcIdx := float64(i) / ratio
		idx := int(srcIdx)
		frac := float32(srcIdx - float64(idx))

		if idx >= len(samples)-1 {
			result[i] = samples[len(samples)-1]
		} else {
			result[i] = samples[idx]*(1-frac) + samples[idx+1]*frac
		}
	}

	return result
}

// WAVInfo метаданные WAV-файла.
type WAVInfo struct {
	SampleRate int
	Channels   int
	BitsPerSample int
	DataSize   int
}

// ParseWAV парсит WAV-файл и возвращает PCM-данные и метаданные.
func ParseWAV(data []byte) (pcmData []byte, sampleRate int, err error) {
	if len(data) < 44 {
		return nil, 0, fmt.Errorf("wav: too short (%d bytes)", len(data))
	}

	// Проверяем RIFF header
	if string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, 0, fmt.Errorf("wav: invalid header")
	}

	// Ищем fmt и data чанки
	offset := 12
	var info WAVInfo
	foundData := false

	for offset < len(data)-8 {
		chunkID := string(data[offset : offset+4])
		chunkSize := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		offset += 8

		switch chunkID {
		case "fmt ":
			if chunkSize < 16 {
				return nil, 0, fmt.Errorf("wav: fmt chunk too small")
			}
			info.Channels = int(binary.LittleEndian.Uint16(data[offset : offset+2]))
			info.SampleRate = int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
			info.BitsPerSample = int(binary.LittleEndian.Uint16(data[offset+14 : offset+16]))
		case "data":
			pcmData = data[offset : offset+chunkSize]
			info.DataSize = chunkSize
			foundData = true
		}

		offset += chunkSize
		if chunkSize%2 != 0 {
			offset++ // padding
		}

		if foundData {
			break
		}
	}

	if !foundData {
		return nil, 0, fmt.Errorf("wav: no data chunk found")
	}

	return pcmData, info.SampleRate, nil
}
