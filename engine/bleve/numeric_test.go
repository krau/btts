package bleve

import (
	"os"
	"testing"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/search/query"
)

func TestNumericRangeQuery(t *testing.T) {
	tmpDir := "test_numeric"
	defer os.RemoveAll(tmpDir)

	// 创建带有字段映射的索引
	docMapping := bleve.NewDocumentMapping()

	// 定义 Type 字段为数值类型
	typeFieldMapping := bleve.NewNumericFieldMapping()
	docMapping.AddFieldMappingsAt("Type", typeFieldMapping)

	// 定义 Text 字段为文本类型
	textFieldMapping := bleve.NewTextFieldMapping()
	docMapping.AddFieldMappingsAt("Text", textFieldMapping)

	mapping := bleve.NewIndexMapping()
	mapping.DefaultMapping = docMapping

	idx, err := bleve.New(tmpDir, mapping)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	// 添加文档
	docs := []struct {
		ID   string
		Type float64
		Text string
	}{
		{"1", 0, "type zero"},
		{"2", 1, "type one"},
		{"3", 2, "type two"},
	}

	for _, doc := range docs {
		if err := idx.Index(doc.ID, doc); err != nil {
			t.Fatal(err)
		}
	}

	// 测试数值范围查询
	t.Run("RangeQueryForZero", func(t *testing.T) {
		val := float64(0)
		q := query.NewNumericRangeInclusiveQuery(&val, &val, boolPtr(true), boolPtr(true))
		q.SetField("Type")

		req := bleve.NewSearchRequest(q)
		req.Fields = []string{"*"}

		result, err := idx.Search(req)
		if err != nil {
			t.Fatal(err)
		}

		t.Logf("Found %d hits for type=0", result.Total)
		for _, hit := range result.Hits {
			t.Logf("  ID=%s, Type=%v", hit.ID, hit.Fields["Type"])
		}

		if result.Total != 1 {
			t.Errorf("Expected 1 hit, got %d", result.Total)
		}
	})

	t.Run("RangeQueryForOne", func(t *testing.T) {
		val := float64(1)
		q := query.NewNumericRangeInclusiveQuery(&val, &val, boolPtr(true), boolPtr(true))
		q.SetField("Type")

		req := bleve.NewSearchRequest(q)
		req.Fields = []string{"*"}

		result, err := idx.Search(req)
		if err != nil {
			t.Fatal(err)
		}

		t.Logf("Found %d hits for type=1", result.Total)

		if result.Total != 1 {
			t.Errorf("Expected 1 hit, got %d", result.Total)
		}
	})
}
