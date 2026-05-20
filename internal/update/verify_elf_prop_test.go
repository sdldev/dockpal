package update

import (
	"os"
	"runtime"
	"testing"

	"pgregory.net/rapid"
)

// Feature: update-mechanism, Property 7: ELF arch verification accepts only matching GOARCH ELF headers.
func TestProperty7_VerifyELFArch(t *testing.T) {
	tmpDir := t.TempDir()
	rapid.Check(t, func(t *rapid.T) {
		header := make([]byte, 20)
		// Magic
		header[0] = 0x7f
		header[1] = 'E'
		header[2] = 'L'
		header[3] = 'F'
		// Class
		class := rapid.IntRange(1, 2).Draw(t, "class")
		header[4] = byte(class)
		// Endianness
		endian := rapid.IntRange(1, 2).Draw(t, "endian")
		header[5] = byte(endian)
		// e_machine at offset 18
		machine := rapid.Uint16().Draw(t, "machine")
		if endian == 1 {
			header[18] = byte(machine)
			header[19] = byte(machine >> 8)
		} else {
			header[18] = byte(machine >> 8)
			header[19] = byte(machine)
		}

		// Write header to temp file
		path := tmpDir + "/elf"
		if err := osWriteFile(path, header, 0644); err != nil {
			t.Fatalf("write failed: %v", err)
		}

		expectedClass, expectedMachine := expectedELFArch(runtime.GOARCH)
		if class == int(expectedClass) && machine == expectedMachine {
			if err := verifyELFArch(path); err != nil {
				t.Fatalf("expected accept, got reject: %v", err)
			}
		} else {
			if err := verifyELFArch(path); err == nil {
				t.Fatalf("expected reject for class=%d machine=%d, got accept", class, machine)
			}
		}
	})
}

// Feature: update-mechanism, Property 7: Non-ELF magic produces verify_not_elf.
func TestProperty7_VerifyNotELF(t *testing.T) {
	tmpDir := t.TempDir()
	rapid.Check(t, func(t *rapid.T) {
		header := rapid.SliceOfN(rapid.Byte(), 20, 20).Draw(t, "header")
		// Ensure magic is not ELF
		header[0] = byte(rapid.IntRange(0, 0x7e).Draw(t, "magic0"))
		if header[0] == 0x7f {
			header[1] = byte(rapid.IntRange(0, 0x44).Draw(t, "magic1"))
		}

		path := tmpDir + "/notelf"
		if err := osWriteFile(path, header, 0644); err != nil {
			t.Fatalf("write failed: %v", err)
		}

		err := verifyELFArch(path)
		if err == nil {
			t.Fatalf("expected verify_not_elf, got nil")
		}
		if !contains(err.Error(), ErrVerifyNotELF) {
			t.Fatalf("expected error containing %q, got %v", ErrVerifyNotELF, err)
		}
	})
}

func osWriteFile(name string, data []byte, perm os.FileMode) error {
	f, err := os.Create(name)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err1 := f.Close(); err1 != nil && err == nil {
		err = err1
	}
	return err
}

func contains(s, substr string) bool { return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr)) }
func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
