package agents

import "os"

func ReadInstruction(filepath string) (string, error) {

	file, err := os.ReadFile(filepath)
	if err != nil {
		return "", err
	}

	return string(file), nil
}
