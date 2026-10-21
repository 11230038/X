package handler

import (
	"net/http"

	"backend/internal/httpx"
	"github.com/gin-gonic/gin"
)

// DocumentText returns a window of one document's extracted text.
//
// The coordinate is the same one every preview in this build uses: characters
// of the text a model was shown, read a window at a time. It is addressed by
// document rather than by file because one file can be read into more than one
// place — the attachment and several knowledge bases — and a preview of this
// row must show the text this row was built from, not whichever document of the
// same bytes happens to sort first.
//
// The base in the path is checked before the read: a document id alone would
// let a client holding one base's path open another base's document, and the
// two would never look wrong afterwards.
func (h *KBHandler) DocumentText(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	document, ok := h.documentIn(c, account, c.Param("kbID"), c.Param("documentID"))
	if !ok {
		return
	}
	offset, err := intParam(c, "offset")
	if err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	limit, err := intParam(c, "limit")
	if err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}

	window, err := h.bases.ReadWindow(c.Request.Context(), document.ID, int64(offset), int64(limit), account)
	if err != nil {
		h.fail(c, "read_document_text_failed", err)
		return
	}
	spans, err := h.bases.ChunkSpans(c.Request.Context(), document.ID, account)
	if err != nil {
		h.fail(c, "read_document_text_failed", err)
		return
	}
	boundaries := make([]chunkSpanView, 0, len(spans))
	for _, span := range spans {
		boundaries = append(boundaries, chunkSpanView{
			Ordinal: span.Ordinal, CharStart: span.CharStart,
			CharEnd: span.CharEnd, Page: span.Page,
		})
	}
	httpx.WriteSuccess(c, http.StatusOK, fileTextView{
		Document:   toDocumentView(window.Document),
		Text:       window.Text,
		Chunks:     boundaries,
		CharStart:  window.CharStart,
		CharEnd:    window.CharEnd,
		TotalChars: window.TotalChars,
	})
}
