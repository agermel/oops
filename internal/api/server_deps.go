package api

import "oops/internal/nodelet"

// findNodelet 按配置 ID 查找 Nodelet。
func (s *Server) findNodelet(id string) (nodelet.NodeletConfig, bool) {
	if s.nodeletManager == nil {
		return nodelet.NodeletConfig{}, false
	}
	return s.nodeletManager.Find(id)
}
