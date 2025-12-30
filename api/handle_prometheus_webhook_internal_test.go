package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCorrelationIDFromLabels(t *testing.T) {
	t.Parallel()

	t.Run("empty labels returns empty string", func(t *testing.T) {
		t.Parallel()
		result := correlationIDFromLabels(map[string]string{})
		assert.Empty(t, result)
	})

	t.Run("nil labels returns empty string", func(t *testing.T) {
		t.Parallel()
		result := correlationIDFromLabels(nil)
		assert.Empty(t, result)
	})

	t.Run("labels without relevant keys returns empty string", func(t *testing.T) {
		t.Parallel()
		labels := map[string]string{
			"foo": "bar",
			"baz": "qux",
		}
		result := correlationIDFromLabels(labels)
		assert.Empty(t, result)
	})

	t.Run("alertname label generates correlation ID", func(t *testing.T) {
		t.Parallel()
		labels := map[string]string{
			"alertname": "TestAlert",
		}
		result := correlationIDFromLabels(labels)
		assert.NotEmpty(t, result)
	})

	t.Run("namespace label generates correlation ID", func(t *testing.T) {
		t.Parallel()
		labels := map[string]string{
			"namespace": "default",
		}
		result := correlationIDFromLabels(labels)
		assert.NotEmpty(t, result)
	})

	t.Run("job label generates correlation ID", func(t *testing.T) {
		t.Parallel()
		labels := map[string]string{
			"job": "prometheus",
		}
		result := correlationIDFromLabels(labels)
		assert.NotEmpty(t, result)
	})

	t.Run("service label generates correlation ID", func(t *testing.T) {
		t.Parallel()
		labels := map[string]string{
			"service": "api-server",
		}
		result := correlationIDFromLabels(labels)
		assert.NotEmpty(t, result)
	})

	t.Run("destination_service_name label generates correlation ID", func(t *testing.T) {
		t.Parallel()
		labels := map[string]string{
			"destination_service_name": "backend",
		}
		result := correlationIDFromLabels(labels)
		assert.NotEmpty(t, result)
	})

	t.Run("monitoring namespace includes instance", func(t *testing.T) {
		t.Parallel()
		labels := map[string]string{
			"namespace": "monitoring",
			"instance":  "localhost:9090",
		}
		result := correlationIDFromLabels(labels)
		assert.NotEmpty(t, result)
	})

	t.Run("non-monitoring namespace excludes instance", func(t *testing.T) {
		t.Parallel()
		labelsWithInstance := map[string]string{
			"namespace": "default",
			"instance":  "localhost:9090",
		}
		labelsWithoutInstance := map[string]string{
			"namespace": "default",
		}
		// Both should produce the same correlation ID since instance is not included for non-monitoring
		resultWith := correlationIDFromLabels(labelsWithInstance)
		resultWithout := correlationIDFromLabels(labelsWithoutInstance)
		assert.Equal(t, resultWith, resultWithout)
	})

	t.Run("multiple labels combined", func(t *testing.T) {
		t.Parallel()
		labels := map[string]string{
			"namespace": "production",
			"alertname": "HighCPU",
			"job":       "node-exporter",
			"service":   "web-server",
		}
		result := correlationIDFromLabels(labels)
		assert.NotEmpty(t, result)
	})
}

func TestCreateLowerCaseKeys(t *testing.T) {
	t.Parallel()

	t.Run("nil map does not panic", func(t *testing.T) {
		t.Parallel()
		assert.NotPanics(t, func() {
			createLowerCaseKeys(nil)
		})
	})

	t.Run("empty map remains empty", func(t *testing.T) {
		t.Parallel()
		m := map[string]string{}
		createLowerCaseKeys(m)
		assert.Empty(t, m)
	})

	t.Run("lowercase keys remain unchanged", func(t *testing.T) {
		t.Parallel()
		m := map[string]string{
			"key": "value",
		}
		createLowerCaseKeys(m)
		assert.Equal(t, "value", m["key"])
		assert.Len(t, m, 1)
	})

	t.Run("uppercase keys get lowercase copy", func(t *testing.T) {
		t.Parallel()
		m := map[string]string{
			"KEY": "value",
		}
		createLowerCaseKeys(m)
		assert.Equal(t, "value", m["KEY"])
		assert.Equal(t, "value", m["key"])
		assert.Len(t, m, 2)
	})

	t.Run("mixed case keys get lowercase copy", func(t *testing.T) {
		t.Parallel()
		m := map[string]string{
			"AlertName": "TestAlert",
			"Severity":  "error",
		}
		createLowerCaseKeys(m)
		assert.Equal(t, "TestAlert", m["alertname"])
		assert.Equal(t, "error", m["severity"])
	})
}

func TestFind(t *testing.T) {
	t.Parallel()

	t.Run("both maps nil returns empty string", func(t *testing.T) {
		t.Parallel()
		result := find(nil, nil, "key")
		assert.Empty(t, result)
	})

	t.Run("first map nil uses second map", func(t *testing.T) {
		t.Parallel()
		map2 := map[string]string{"key": "value2"}
		result := find(nil, map2, "key")
		assert.Equal(t, "value2", result)
	})

	t.Run("second map nil uses first map", func(t *testing.T) {
		t.Parallel()
		map1 := map[string]string{"key": "value1"}
		result := find(map1, nil, "key")
		assert.Equal(t, "value1", result)
	})

	t.Run("first map takes precedence", func(t *testing.T) {
		t.Parallel()
		map1 := map[string]string{"key": "value1"}
		map2 := map[string]string{"key": "value2"}
		result := find(map1, map2, "key")
		assert.Equal(t, "value1", result)
	})

	t.Run("falls back to second map if key missing in first", func(t *testing.T) {
		t.Parallel()
		map1 := map[string]string{"other": "value1"}
		map2 := map[string]string{"key": "value2"}
		result := find(map1, map2, "key")
		assert.Equal(t, "value2", result)
	})

	t.Run("multiple keys tries in order", func(t *testing.T) {
		t.Parallel()
		map1 := map[string]string{"third": "value"}
		result := find(map1, nil, "first", "second", "third")
		assert.Equal(t, "value", result)
	})

	t.Run("trims whitespace from values", func(t *testing.T) {
		t.Parallel()
		map1 := map[string]string{"key": "  value  "}
		result := find(map1, nil, "key")
		assert.Equal(t, "value", result)
	})

	t.Run("key not found returns empty string", func(t *testing.T) {
		t.Parallel()
		map1 := map[string]string{"other": "value"}
		result := find(map1, nil, "key")
		assert.Empty(t, result)
	})
}

func TestValueOrDefault(t *testing.T) {
	t.Parallel()

	t.Run("non-empty value returns value", func(t *testing.T) {
		t.Parallel()
		result := valueOrDefault("value", "default")
		assert.Equal(t, "value", result)
	})

	t.Run("empty value returns default", func(t *testing.T) {
		t.Parallel()
		result := valueOrDefault("", "default")
		assert.Equal(t, "default", result)
	})

	t.Run("whitespace only is not empty", func(t *testing.T) {
		t.Parallel()
		result := valueOrDefault("  ", "default")
		assert.Equal(t, "  ", result)
	})
}
