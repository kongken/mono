package share

import (
	"bytes"
	"sync"
	"testing"
)

func TestRendererConcurrentPublicPreviewsAreIndependent(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	var contents [2]*Content
	var expected [2][]byte
	for i := range contents {
		contents[i] = sampleContent()
		contents[i].Stats.Cities += i
		expected[i], err = renderer.Render(contents[i])
		if err != nil {
			t.Fatal(err)
		}
	}
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := range contents {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			<-start
			for range 2 {
				got, err := renderer.Render(contents[index])
				if err != nil {
					t.Errorf("concurrent preview %d: %v", index, err)
					return
				}
				if !bytes.Equal(got, expected[index]) {
					t.Errorf("concurrent preview %d changed another visitor's image", index)
					return
				}
			}
		}(i)
	}
	close(start)
	workers.Wait()
}
