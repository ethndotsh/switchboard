//go:build !tinygo && !wasip1 && !wasm

package guest

func requestLen() uint32 {
	return 0
}

func readRequest(uint32) {}

func writeAction(uint32, uint32) {}
