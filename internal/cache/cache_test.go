package cache

import "testing"

func TestLoadCache(t *testing.T) {
	err := LoadCache()
	if err != nil {
		t.Errorf("load cache error: %v \n", err)
	}

	if Set("config_path", "test_config_path") != nil {
		t.Errorf("set cache error: %v \n", err)
	}

	path := Get("config_path")

	t.Log("get config_path:", path)
}
