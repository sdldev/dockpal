package update

import (
	"encoding/binary"
	"fmt"
	"os"
	"runtime"
)

// verifyELFArch reads the first 20 bytes of path and checks that the ELF
// e_machine field matches the host architecture (R2.5, R2.8, R2.9).
func verifyELFArch(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("%s: failed to open %s: %w", ErrVerifyNotELF, path, err)
	}
	defer f.Close()

	header := make([]byte, 20)
	if _, err := f.Read(header); err != nil {
		return fmt.Errorf("%s: failed to read ELF header from %s: %w", ErrVerifyNotELF, path, err)
	}

	// Check magic at offset 0.
	magic := binary.LittleEndian.Uint32(header[0:4])
	if magic != elfMagic {
		return fmt.Errorf("%s: %s is not a valid ELF executable", ErrVerifyNotELF, path)
	}

	// Class at offset 4.
	class := header[4]
	// Endianness at offset 5.
	endian := header[5]
	// e_machine at offset 18 (uint16).
	var eMachine uint16
	switch endian {
	case 1: // ELFDATA2LSB
		eMachine = binary.LittleEndian.Uint16(header[18:20])
	case 2: // ELFDATA2MSB
		eMachine = binary.BigEndian.Uint16(header[18:20])
	default:
		return fmt.Errorf("%s: unknown ELF endianness %d in %s", ErrVerifyArchMismatch, endian, path)
	}

	expectedClass, expectedMachine := expectedELFArch(runtime.GOARCH)
	if class != expectedClass || eMachine != expectedMachine {
		return fmt.Errorf("%s: host %s does not match binary ELF class=%d e_machine=%d",
			ErrVerifyArchMismatch, runtime.GOARCH, class, eMachine)
	}

	return nil
}

func expectedELFArch(goarch string) (class byte, machine uint16) {
	switch goarch {
	case "amd64":
		return 2, EM_X86_64 // ELFCLASS64, EM_X86_64
	case "arm64":
		return 2, EM_AARCH64 // ELFCLASS64, EM_AARCH64
	case "arm":
		return 1, EM_ARM // ELFCLASS32, EM_ARM
	case "386":
		return 1, EM_386 // ELFCLASS32, EM_386
	}
	// Unknown architecture: return impossible values so verification fails.
	return 0xFF, 0xFFFF
}
