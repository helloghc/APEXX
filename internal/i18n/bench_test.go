package i18n

import "testing"

func BenchmarkPhraseMiss(b *testing.B) {
	look := Builtin("ja")
	for i := 0; i < b.N; i++ {
		Phrase("Sudadera Pacífico 2 Talla 25", look)
		Phrase("Llega entre el vie 13 nov y el mié 18 nov", look)
	}
}
