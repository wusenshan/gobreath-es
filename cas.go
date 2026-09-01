package es

import "strconv"

// DocMeta 是单个文档的 ES 原生元信息，主要来自 GET / _search 响应里的
// _id / _seq_no / _primary_term 等顶层字段。
//
// _seq_no 与 _primary_term 配合 IfSeqNoPrimaryTerm 即可实现乐观并发控制（OCC）：
// 读取时记下这两个值，写入时回传，若期间被别人改动过则 ES 返回 409 冲突。
type DocMeta struct {
	ID          string // 文档 _id（GET / Search 回填）
	SeqNo       int64  // 文档当前序列号，每次写入自增
	PrimaryTerm int64  // 主分片代际，重启/故障切换时变化
}

// writeOpts 收集写入类操作的可选行为（乐观并发控制等）。
type writeOpts struct {
	hasCAS       bool
	ifSeqNo      int64
	ifPrimaryTerm int64
}

// WriteOption 是写入类操作（Index/Update/Delete）的可选行为修饰器。
// 以可变参数形式传入，不传则保持默认语义（无乐观并发校验）。
type WriteOption func(*writeOpts)

// IfSeqNoPrimaryTerm 启用乐观并发控制：仅当目标文档当前的
// _seq_no 与 _primary_term 与给定值一致时才允许写入，否则返回错误。
// 典型用法：GetWithMeta 读取到 DocMeta，修改后通过 Index/Update/Delete 回传。
func IfSeqNoPrimaryTerm(seqNo, primaryTerm int64) WriteOption {
	return func(o *writeOpts) {
		o.hasCAS = true
		o.ifSeqNo = seqNo
		o.ifPrimaryTerm = primaryTerm
	}
}

// applyWriteOpts 把 writeOpts 转成可读诊断串（用于日志/错误）。
func (o writeOpts) String() string {
	if !o.hasCAS {
		return ""
	}
	return "if_seq_no=" + strconv.FormatInt(o.ifSeqNo, 10) +
		",if_primary_term=" + strconv.FormatInt(o.ifPrimaryTerm, 10)
}
