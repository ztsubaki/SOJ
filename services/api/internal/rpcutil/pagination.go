// Package rpcutil contains shared unary RPC conversion helpers.
package rpcutil

import sojv1 "github.com/ztsubaki/soj/proto/soj/v1"

// Pagination applies the common defaults and page-size limit.
func Pagination(request *sojv1.PageRequest) (int, int) {
	page, size := 1, 20
	if request.GetPage() > 0 {
		page = int(request.GetPage())
	}
	if request.GetPageSize() >= 1 && request.GetPageSize() <= 100 {
		size = int(request.GetPageSize())
	}
	return page, size
}
