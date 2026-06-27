package checker

import (
	"context"

	"oops/internal/connection"
)

const ElasticsearchType = "elasticsearch"

// ElasticsearchChecker 检查 Elasticsearch 或 OpenSearch 是否可访问。
type ElasticsearchChecker struct {
	httpChecker *HTTPChecker
}

// NewElasticsearchChecker 创建 Elasticsearch 健康检查器。
func NewElasticsearchChecker() *ElasticsearchChecker {
	return &ElasticsearchChecker{
		httpChecker: NewHTTPChecker(ElasticsearchType, "/_cluster/health"),
	}
}

// Type 返回 Elasticsearch 连接类型。
func (c *ElasticsearchChecker) Type() string {
	return ElasticsearchType
}

// Check 请求 /_cluster/health，并根据响应状态码判断连接状态。
func (c *ElasticsearchChecker) Check(ctx context.Context, conn connection.Connection) connection.Result {
	return c.httpChecker.Check(ctx, conn)
}
