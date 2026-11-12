// Package evalset is the search evaluation of Apexx: a Spanish catalog of a
// general store and the searches real shoppers type, each labeled with the
// products that answer it (2 = exactly what was asked, 1 = acceptable).
//
// Every search change (IA-1 hybrid search, re-ranking, a new embeddings
// model) is measured against it with nDCG@10 and recall@50, by kind of
// search, and must not lose against the version without AI.
package evalset

// Product of the evaluation catalog.
type Product struct {
	Handle, Title, Type, Category, Brand string
	Tags                                 []string
	Desc                                 string
}

// Query is a labeled search.
type Query struct {
	Q    string
	Kind string // exacta, error, plural, parcial, sinonimo, intencion, sku
	Rel  map[string]int
}

// Catalog is the evaluation store.
var Catalog = []Product{
	// Ropa
	{"chamarra-impermeable-negra", "Chamarra impermeable negra", "Chamarra", "Ropa > Chamarras", "Andes", []string{"lluvia", "exterior"}, "Chamarra con capucha y costuras selladas. Repele el agua en lluvia intensa."},
	{"chamarra-rompevientos-azul", "Chamarra rompevientos azul", "Chamarra", "Ropa > Chamarras", "Andes", []string{"viento"}, "Ligera y plegable, ideal para correr en días con viento."},
	{"chamarra-plumas-invierno", "Chamarra de plumas para invierno", "Chamarra", "Ropa > Chamarras", "Polar", []string{"frío", "nieve"}, "Relleno de pluma de ganso, abriga hasta -10 °C."},
	{"impermeable-poncho-amarillo", "Poncho para lluvia amarillo", "Impermeable", "Ropa > Impermeables", "Andes", []string{"lluvia"}, "Poncho de PVC con capucha, cubre la mochila."},
	{"sudadera-algodon-gris", "Sudadera de algodón gris", "Sudadera", "Ropa > Sudaderas", "Urbana", []string{"casual"}, "Sudadera con capucha y bolsa canguro, 100 % algodón."},
	{"sudadera-deportiva-negra", "Sudadera deportiva negra", "Sudadera", "Ropa > Sudaderas", "Ritmo", []string{"deporte"}, "Tela técnica que seca rápido, para entrenar."},
	{"playera-algodon-blanca", "Playera de algodón blanca", "Playera", "Ropa > Playeras", "Urbana", []string{"básico"}, "Playera de cuello redondo, algodón peinado."},
	{"playera-dry-fit-roja", "Playera deportiva dry fit roja", "Playera", "Ropa > Playeras", "Ritmo", []string{"deporte", "correr"}, "Tela que absorbe el sudor, para correr y entrenar."},
	{"camisa-lino-blanca", "Camisa de lino blanca", "Camisa", "Ropa > Camisas", "Costa", []string{"verano", "fresca"}, "Camisa de manga larga en lino, fresca para clima caluroso."},
	{"camisa-mezclilla", "Camisa de mezclilla", "Camisa", "Ropa > Camisas", "Urbana", []string{"casual"}, "Camisa de mezclilla ligera con botones de presión."},
	{"pantalon-mezclilla-azul", "Pantalón de mezclilla azul", "Pantalón", "Ropa > Pantalones", "Urbana", []string{"jeans"}, "Jeans de corte recto, mezclilla elástica."},
	{"pantalon-cargo-verde", "Pantalón cargo verde olivo", "Pantalón", "Ropa > Pantalones", "Andes", []string{"exterior"}, "Pantalón con bolsas laterales, tela resistente para campismo."},
	{"pants-deportivo", "Pants deportivo negro", "Pantalón", "Ropa > Pantalones", "Ritmo", []string{"deporte"}, "Pantalón deportivo con resorte y puños ajustables."},
	{"short-correr", "Short para correr", "Short", "Ropa > Shorts", "Ritmo", []string{"correr"}, "Short ligero con licra interior y bolsa para llaves."},
	{"vestido-floral", "Vestido floral de verano", "Vestido", "Ropa > Vestidos", "Costa", []string{"verano"}, "Vestido midi con estampado de flores, tela fresca."},
	{"calcetas-deportivas", "Calcetas deportivas (3 pares)", "Calcetas", "Ropa > Calcetines", "Ritmo", []string{"deporte"}, "Calcetas acolchadas que evitan ampollas."},
	{"gorra-beisbol", "Gorra de béisbol negra", "Gorra", "Accesorios > Gorras", "Urbana", []string{"sol"}, "Gorra ajustable de seis paneles."},
	{"bufanda-lana", "Bufanda de lana", "Bufanda", "Accesorios > Invierno", "Polar", []string{"frío"}, "Bufanda tejida de lana merino."},
	{"guantes-invierno", "Guantes térmicos de invierno", "Guantes", "Accesorios > Invierno", "Polar", []string{"frío", "nieve"}, "Guantes con forro polar, táctiles para el celular."},
	// Calzado
	{"tenis-running-rojos", "Tenis para correr rojos", "Tenis", "Calzado > Tenis", "Ritmo", []string{"running", "correr"}, "Amortiguación ligera para carreras de 10 km."},
	{"tenis-running-negros", "Tenis para correr negros", "Tenis", "Calzado > Tenis", "Ritmo", []string{"running", "correr"}, "Suela con buen agarre en pavimento mojado."},
	{"tenis-casuales-blancos", "Tenis casuales blancos", "Tenis", "Calzado > Tenis", "Urbana", []string{"casual"}, "Tenis de piel sintética para diario."},
	{"botas-montana", "Botas de montaña impermeables", "Botas", "Calzado > Botas", "Andes", []string{"senderismo", "lluvia"}, "Botas con membrana impermeable y suela Vibram para senderismo."},
	{"botas-lluvia", "Botas de lluvia de hule", "Botas", "Calzado > Botas", "Andes", []string{"lluvia"}, "Botas de hule a media pierna, no dejan pasar el agua."},
	{"sandalias-playa", "Sandalias de playa", "Sandalias", "Calzado > Sandalias", "Costa", []string{"verano", "playa"}, "Sandalias de hule ligeras para alberca y playa."},
	{"zapatos-vestir-cafe", "Zapatos de vestir café", "Zapatos", "Calzado > Zapatos", "Clásico", []string{"formal"}, "Zapato tipo Oxford de piel."},
	{"pantuflas-afelpadas", "Pantuflas afelpadas", "Pantuflas", "Calzado > Pantuflas", "Polar", []string{"casa", "frío"}, "Pantuflas suaves con suela antiderrapante."},
	// Hogar
	{"sarten-antiadherente-28", "Sartén antiadherente 28 cm", "Sartén", "Hogar > Cocina", "Cocina Pro", []string{"cocina"}, "Sartén de aluminio con recubrimiento antiadherente, apta para inducción."},
	{"olla-presion-6l", "Olla de presión 6 litros", "Olla", "Hogar > Cocina", "Cocina Pro", []string{"cocina"}, "Olla exprés de acero inoxidable con válvula de seguridad."},
	{"juego-cuchillos", "Juego de cuchillos de cocina (5 piezas)", "Cuchillos", "Hogar > Cocina", "Cocina Pro", []string{"cocina"}, "Cuchillos de acero inoxidable con base de madera."},
	{"licuadora-600w", "Licuadora 600 W", "Licuadora", "Hogar > Electrodomésticos", "Hogar Max", []string{"cocina"}, "Licuadora de vaso de vidrio, 5 velocidades, tritura hielo."},
	{"cafetera-goteo", "Cafetera de goteo 12 tazas", "Cafetera", "Hogar > Electrodomésticos", "Hogar Max", []string{"café"}, "Cafetera programable con jarra de vidrio."},
	{"freidora-aire", "Freidora de aire 4 litros", "Freidora", "Hogar > Electrodomésticos", "Hogar Max", []string{"cocina", "saludable"}, "Cocina sin aceite con aire caliente."},
	{"sabanas-matrimonial", "Juego de sábanas matrimonial", "Sábanas", "Hogar > Recámara", "Descanso", []string{"cama"}, "Sábanas de microfibra, incluye fundas."},
	{"almohada-memory", "Almohada viscoelástica", "Almohada", "Hogar > Recámara", "Descanso", []string{"cama", "dormir"}, "Almohada de memory foam que se adapta al cuello."},
	{"cobija-borrega", "Cobija de borrega king size", "Cobija", "Hogar > Recámara", "Descanso", []string{"frío", "cama"}, "Cobija gruesa y suave para noches frías."},
	{"lampara-escritorio-led", "Lámpara de escritorio LED", "Lámpara", "Hogar > Iluminación", "Luz Viva", []string{"oficina", "estudio"}, "Luz regulable con brazo flexible y puerto USB."},
	{"foco-led-inteligente", "Foco LED inteligente", "Foco", "Hogar > Iluminación", "Luz Viva", []string{"wifi"}, "Foco de colores controlado desde el celular."},
	{"cortina-blackout", "Cortina blackout gris", "Cortina", "Hogar > Decoración", "Descanso", []string{"oscuridad"}, "Bloquea la luz del sol, ideal para dormir de día."},
	{"tapete-sala", "Tapete para sala 160 × 230", "Tapete", "Hogar > Decoración", "Descanso", []string{"sala"}, "Tapete de pelo corto, fácil de limpiar."},
	// Ferretería
	{"taladro-percutor", "Taladro percutor 650 W", "Taladro", "Ferretería > Herramienta eléctrica", "Fuerza", []string{"concreto"}, "Taladro con percusión para concreto, madera y metal."},
	{"taladro-inalambrico", "Taladro atornillador inalámbrico 20 V", "Taladro", "Ferretería > Herramienta eléctrica", "Fuerza", []string{"batería"}, "Con dos baterías de litio y cargador rápido."},
	{"juego-brocas", "Juego de brocas (20 piezas)", "Brocas", "Ferretería > Accesorios", "Fuerza", []string{"taladro"}, "Brocas para concreto, madera y metal."},
	{"desarmadores", "Juego de desarmadores (8 piezas)", "Desarmadores", "Ferretería > Herramienta manual", "Fuerza", []string{"tornillos"}, "Desarmadores de cruz y planos con punta magnética."},
	{"martillo-una", "Martillo de uña 16 oz", "Martillo", "Ferretería > Herramienta manual", "Fuerza", []string{"clavos"}, "Mango de fibra de vidrio antivibración."},
	{"cinta-metrica", "Cinta métrica 5 m", "Cinta métrica", "Ferretería > Medición", "Fuerza", []string{"medir"}, "Flexómetro con freno y gancho magnético."},
	{"escalera-aluminio", "Escalera de aluminio 6 peldaños", "Escalera", "Ferretería > Escaleras", "Fuerza", []string{"altura"}, "Escalera de tijera ligera, soporta 120 kg."},
	{"pintura-vinilica-blanca", "Pintura vinílica blanca 19 L", "Pintura", "Ferretería > Pintura", "Color Total", []string{"pared"}, "Pintura lavable para interiores, acabado mate."},
	{"rodillo-pintar", "Rodillo para pintar 9 pulgadas", "Rodillo", "Ferretería > Pintura", "Color Total", []string{"pared"}, "Rodillo de felpa con charola."},
	{"extension-electrica", "Extensión eléctrica 10 m", "Extensión", "Ferretería > Electricidad", "Fuerza", []string{"cable"}, "Extensión con tres contactos y tierra física."},
	// Electrónica
	{"audifonos-inalambricos", "Audífonos inalámbricos con cancelación de ruido", "Audífonos", "Electrónica > Audio", "Sonora", []string{"bluetooth"}, "Hasta 30 horas de batería, cancelación activa de ruido."},
	{"audifonos-deportivos", "Audífonos deportivos resistentes al sudor", "Audífonos", "Electrónica > Audio", "Sonora", []string{"bluetooth", "correr"}, "Ganchos para la oreja, no se caen al correr."},
	{"bocina-bluetooth", "Bocina Bluetooth portátil", "Bocina", "Electrónica > Audio", "Sonora", []string{"bluetooth", "fiesta"}, "Resistente al agua, 12 horas de música."},
	{"cargador-rapido", "Cargador rápido USB-C 30 W", "Cargador", "Electrónica > Accesorios", "Volt", []string{"celular"}, "Carga el celular al 50 % en 30 minutos."},
	{"cable-usb-c", "Cable USB-C a USB-C 2 m", "Cable", "Electrónica > Accesorios", "Volt", []string{"celular"}, "Cable trenzado reforzado."},
	{"power-bank", "Batería portátil 20 000 mAh", "Batería portátil", "Electrónica > Accesorios", "Volt", []string{"celular", "viaje"}, "Carga dos teléfonos a la vez."},
	{"funda-celular", "Funda de silicón para celular", "Funda", "Electrónica > Accesorios", "Volt", []string{"celular"}, "Protege contra golpes y caídas."},
	{"smartwatch", "Reloj inteligente con GPS", "Reloj", "Electrónica > Wearables", "Ritmo", []string{"correr", "deporte"}, "Mide ritmo cardiaco, pasos y rutas con GPS."},
	{"teclado-inalambrico", "Teclado inalámbrico en español", "Teclado", "Electrónica > Cómputo", "Teclea", []string{"oficina"}, "Teclado con ñ, silencioso, batería de un año."},
	{"mouse-ergonomico", "Mouse ergonómico vertical", "Mouse", "Electrónica > Cómputo", "Teclea", []string{"oficina"}, "Evita dolor de muñeca en jornadas largas."},
	{"monitor-24", "Monitor 24 pulgadas Full HD", "Monitor", "Electrónica > Cómputo", "Teclea", []string{"oficina"}, "Panel IPS con bordes delgados."},
	// Deportes y exterior
	{"tapete-yoga", "Tapete de yoga antiderrapante", "Tapete", "Deportes > Yoga", "Zen", []string{"ejercicio"}, "Tapete de 6 mm, no se resbala."},
	{"mancuernas-5kg", "Mancuernas de 5 kg (par)", "Mancuernas", "Deportes > Pesas", "Fuerza Fit", []string{"ejercicio", "gimnasio"}, "Mancuernas recubiertas de neopreno."},
	{"bicicleta-montana", "Bicicleta de montaña rodada 29", "Bicicleta", "Deportes > Ciclismo", "Ruta", []string{"exterior"}, "Cuadro de aluminio, 21 velocidades, frenos de disco."},
	{"casco-ciclismo", "Casco para ciclismo", "Casco", "Deportes > Ciclismo", "Ruta", []string{"seguridad"}, "Casco ventilado y ajustable."},
	{"tienda-campana-4", "Tienda de campaña para 4 personas", "Tienda de campaña", "Deportes > Campismo", "Andes", []string{"exterior", "lluvia"}, "Impermeable, se arma en 5 minutos."},
	{"bolsa-dormir", "Bolsa de dormir para 5 °C", "Bolsa de dormir", "Deportes > Campismo", "Andes", []string{"exterior", "frío"}, "Bolsa de dormir tipo momia, compacta."},
	{"mochila-senderismo-40", "Mochila de senderismo 40 L", "Mochila", "Deportes > Campismo", "Andes", []string{"exterior", "viaje"}, "Espalda ventilada y cubierta para lluvia."},
	{"termo-acero", "Termo de acero inoxidable 1 L", "Termo", "Deportes > Campismo", "Andes", []string{"café", "viaje"}, "Mantiene bebidas calientes 24 horas."},
	{"balon-futbol", "Balón de futbol número 5", "Balón", "Deportes > Futbol", "Gol", []string{"futbol"}, "Balón cosido a máquina para pasto."},
	{"espinilleras", "Espinilleras de futbol", "Espinilleras", "Deportes > Futbol", "Gol", []string{"futbol"}, "Protección ligera con correa."},
	// Belleza y cuidado
	{"protector-solar-50", "Protector solar FPS 50", "Protector solar", "Belleza > Cuidado de la piel", "Sol Sano", []string{"sol", "playa"}, "Protección UVA/UVB resistente al agua."},
	{"crema-hidratante", "Crema hidratante facial", "Crema", "Belleza > Cuidado de la piel", "Piel Pura", []string{"piel seca"}, "Con ácido hialurónico, para piel seca."},
	{"shampoo-anticaspa", "Shampoo anticaspa 400 ml", "Shampoo", "Belleza > Cabello", "Brillo", []string{"cabello"}, "Controla la caspa desde la primera aplicación."},
	{"secadora-cabello", "Secadora de cabello 2000 W", "Secadora", "Belleza > Cabello", "Brillo", []string{"cabello"}, "Con difusor y aire frío."},
	{"cepillo-dientes-electrico", "Cepillo de dientes eléctrico", "Cepillo", "Belleza > Higiene", "Sonrisa", []string{"dientes"}, "Recargable, con temporizador de dos minutos."},
	{"perfume-citrico", "Perfume cítrico 100 ml", "Perfume", "Belleza > Fragancias", "Aroma", []string{"fresco"}, "Notas de limón y bergamota."},
	// Mascotas
	{"croquetas-perro-adulto", "Croquetas para perro adulto 15 kg", "Alimento", "Mascotas > Perros", "Can", []string{"perro"}, "Alimento completo para perros adultos de raza mediana."},
	{"croquetas-gato", "Alimento para gato 3 kg", "Alimento", "Mascotas > Gatos", "Felino", []string{"gato"}, "Croquetas con salmón para gatos adultos."},
	{"arenero-gato", "Arenero cubierto para gato", "Arenero", "Mascotas > Gatos", "Felino", []string{"gato"}, "Con filtro de carbón contra olores."},
	{"correa-perro", "Correa retráctil para perro", "Correa", "Mascotas > Perros", "Can", []string{"perro", "paseo"}, "Correa de 5 m con freno."},
	{"cama-perro", "Cama para perro mediana", "Cama", "Mascotas > Perros", "Can", []string{"perro", "dormir"}, "Cama acolchada con funda lavable."},
	{"rascador-gato", "Rascador para gato con torre", "Rascador", "Mascotas > Gatos", "Felino", []string{"gato"}, "Torre de 1 m con cuerda de sisal."},
	// Papelería y oficina
	{"cuaderno-profesional", "Cuaderno profesional raya 100 hojas", "Cuaderno", "Papelería > Cuadernos", "Escolar", []string{"escuela"}, "Cuaderno cosido, pasta dura."},
	{"plumas-gel", "Plumas de gel (10 colores)", "Plumas", "Papelería > Escritura", "Escolar", []string{"escuela"}, "Tinta de gel que no se corre."},
	{"mochila-escolar", "Mochila escolar con compartimento para laptop", "Mochila", "Papelería > Mochilas", "Escolar", []string{"escuela", "laptop"}, "Mochila con espalda acolchada y bolsa para laptop de 15″."},
	{"silla-oficina", "Silla de oficina ergonómica", "Silla", "Oficina > Muebles", "Teclea", []string{"oficina"}, "Respaldo de malla y soporte lumbar ajustable."},
	{"escritorio-plegable", "Escritorio plegable", "Escritorio", "Oficina > Muebles", "Teclea", []string{"oficina", "casa"}, "Escritorio compacto para trabajar en casa."},
	// Juguetes y bebés
	{"bloques-construccion", "Bloques de construcción (500 piezas)", "Juguete", "Juguetes > Construcción", "Juega", []string{"niños"}, "Bloques compatibles para mayores de 4 años."},
	{"rompecabezas-1000", "Rompecabezas de 1000 piezas", "Rompecabezas", "Juguetes > Juegos de mesa", "Juega", []string{"familia"}, "Paisaje de montaña."},
	{"carriola-ligera", "Carriola ligera plegable", "Carriola", "Bebés > Paseo", "Pasito", []string{"bebé", "viaje"}, "Se pliega con una mano, pesa 6 kg."},
	{"panales-etapa-3", "Pañales etapa 3 (40 piezas)", "Pañales", "Bebés > Higiene", "Pasito", []string{"bebé"}, "Pañales con barreras antiescurrimiento."},
	{"biberon-anticolicos", "Biberón anticólicos 260 ml", "Biberón", "Bebés > Alimentación", "Pasito", []string{"bebé"}, "Válvula que reduce el aire que traga el bebé."},
}

// Queries are the labeled searches.
var Queries = []Query{
	// exacta: the words of the catalog
	{"chamarra impermeable", "exacta", map[string]int{"chamarra-impermeable-negra": 2, "impermeable-poncho-amarillo": 1, "chamarra-rompevientos-azul": 1, "chamarra-plumas-invierno": 1}},
	{"taladro percutor", "exacta", map[string]int{"taladro-percutor": 2, "taladro-inalambrico": 1}},
	{"sartén antiadherente", "exacta", map[string]int{"sarten-antiadherente-28": 2}},
	{"tenis para correr", "exacta", map[string]int{"tenis-running-rojos": 2, "tenis-running-negros": 2}},
	{"bocina bluetooth", "exacta", map[string]int{"bocina-bluetooth": 2, "audifonos-inalambricos": 1, "audifonos-deportivos": 1}},
	{"protector solar", "exacta", map[string]int{"protector-solar-50": 2}},
	{"silla de oficina", "exacta", map[string]int{"silla-oficina": 2, "escritorio-plegable": 1}},
	{"cama para perro", "exacta", map[string]int{"cama-perro": 2}},
	{"camisa de lino", "exacta", map[string]int{"camisa-lino-blanca": 2}},
	{"mochila de senderismo", "exacta", map[string]int{"mochila-senderismo-40": 2}},
	// error: typos
	{"chamara impermiable", "error", map[string]int{"chamarra-impermeable-negra": 2, "impermeable-poncho-amarillo": 1}},
	{"taladro percutr", "error", map[string]int{"taladro-percutor": 2, "taladro-inalambrico": 1}},
	{"audifonos inalambrcos", "error", map[string]int{"audifonos-inalambricos": 2, "audifonos-deportivos": 1}},
	{"licuadra", "error", map[string]int{"licuadora-600w": 2}},
	{"bisicleta montaña", "error", map[string]int{"bicicleta-montana": 2}},
	{"almoada", "error", map[string]int{"almohada-memory": 2}},
	{"carreola", "error", map[string]int{"carriola-ligera": 2}},
	{"cafetera de goteo", "exacta", map[string]int{"cafetera-goteo": 2}},
	// plural / singular and gender
	{"chamarras", "plural", map[string]int{"chamarra-impermeable-negra": 2, "chamarra-rompevientos-azul": 2, "chamarra-plumas-invierno": 2}},
	{"sudaderas negras", "plural", map[string]int{"sudadera-deportiva-negra": 2, "sudadera-algodon-gris": 1}},
	{"pantalones", "plural", map[string]int{"pantalon-mezclilla-azul": 2, "pantalon-cargo-verde": 2, "pants-deportivo": 1}},
	{"botas impermeables", "plural", map[string]int{"botas-montana": 2, "botas-lluvia": 2}},
	{"lámparas", "plural", map[string]int{"lampara-escritorio-led": 2}},
	{"focos", "plural", map[string]int{"foco-led-inteligente": 2}},
	// parcial: the start of a word, as typed while searching
	{"impermea", "parcial", map[string]int{"chamarra-impermeable-negra": 2, "impermeable-poncho-amarillo": 2, "botas-montana": 1}},
	{"mancuer", "parcial", map[string]int{"mancuernas-5kg": 2}},
	{"rompeca", "parcial", map[string]int{"rompecabezas-1000": 2}},
	{"desarma", "parcial", map[string]int{"desarmadores": 2}},
	// sinonimo: same thing, another word (Mexican Spanish and others)
	{"jeans", "sinonimo", map[string]int{"pantalon-mezclilla-azul": 2}},
	{"zapatillas para correr", "sinonimo", map[string]int{"tenis-running-rojos": 2, "tenis-running-negros": 2}},
	{"playera", "exacta", map[string]int{"playera-algodon-blanca": 2, "playera-dry-fit-roja": 2}},
	{"camiseta deportiva", "sinonimo", map[string]int{"playera-dry-fit-roja": 2, "sudadera-deportiva-negra": 1}},
	{"cascos inalámbricos", "sinonimo", map[string]int{"audifonos-inalambricos": 2, "audifonos-deportivos": 1}},
	{"olla exprés", "sinonimo", map[string]int{"olla-presion-6l": 2}},
	{"flexómetro", "sinonimo", map[string]int{"cinta-metrica": 2}},
	{"destornilladores", "sinonimo", map[string]int{"desarmadores": 2}},
	{"computadora portátil mochila", "sinonimo", map[string]int{"mochila-escolar": 2}},
	{"cobertor", "sinonimo", map[string]int{"cobija-borrega": 2}},
	{"alfombra", "sinonimo", map[string]int{"tapete-sala": 2, "tapete-yoga": 1}},
	{"cargador de celular", "exacta", map[string]int{"cargador-rapido": 2, "power-bank": 1, "cable-usb-c": 1}},
	// intencion: what for, not what
	{"algo para la lluvia", "intencion", map[string]int{"chamarra-impermeable-negra": 2, "impermeable-poncho-amarillo": 2, "botas-lluvia": 2, "botas-montana": 1, "tienda-campana-4": 1}},
	{"para el frío", "intencion", map[string]int{"chamarra-plumas-invierno": 2, "guantes-invierno": 2, "bufanda-lana": 2, "cobija-borrega": 1, "pantuflas-afelpadas": 1}},
	{"ir a correr", "intencion", map[string]int{"tenis-running-rojos": 2, "tenis-running-negros": 2, "short-correr": 2, "playera-dry-fit-roja": 1, "audifonos-deportivos": 1, "smartwatch": 1}},
	{"acampar", "intencion", map[string]int{"tienda-campana-4": 2, "bolsa-dormir": 2, "mochila-senderismo-40": 1, "termo-acero": 1}},
	{"trabajar desde casa", "intencion", map[string]int{"escritorio-plegable": 2, "silla-oficina": 2, "monitor-24": 1, "teclado-inalambrico": 1, "mouse-ergonomico": 1, "lampara-escritorio-led": 1}},
	{"regalo para mi perro", "intencion", map[string]int{"cama-perro": 2, "correa-perro": 2, "croquetas-perro-adulto": 1}},
	{"cocinar sin aceite", "intencion", map[string]int{"freidora-aire": 2}},
	{"dormir mejor", "intencion", map[string]int{"almohada-memory": 2, "cortina-blackout": 2, "sabanas-matrimonial": 1, "cobija-borrega": 1}},
	{"ir a la playa", "intencion", map[string]int{"sandalias-playa": 2, "protector-solar-50": 2, "vestido-floral": 1}},
	{"pintar mi cuarto", "intencion", map[string]int{"pintura-vinilica-blanca": 2, "rodillo-pintar": 2, "escalera-aluminio": 1}},
	{"hacer ejercicio en casa", "intencion", map[string]int{"tapete-yoga": 2, "mancuernas-5kg": 2}},
	{"regreso a clases", "intencion", map[string]int{"cuaderno-profesional": 2, "plumas-gel": 2, "mochila-escolar": 2}},
	{"para el bebé", "intencion", map[string]int{"carriola-ligera": 2, "panales-etapa-3": 2, "biberon-anticolicos": 2}},
	// sku-like and brand
	{"andes", "exacta", map[string]int{"chamarra-impermeable-negra": 1, "chamarra-rompevientos-azul": 1, "impermeable-poncho-amarillo": 1, "pantalon-cargo-verde": 1, "botas-montana": 1, "botas-lluvia": 1, "tienda-campana-4": 1, "bolsa-dormir": 1, "mochila-senderismo-40": 1, "termo-acero": 1}},
	{"20 000 mah", "exacta", map[string]int{"power-bank": 2}},
}

// Score is the quality of one ranking function over the queries.
type Score struct {
	NDCG10   float64            `json:"ndcg10"`
	Recall50 float64            `json:"recall50"`
	Empty    float64            `json:"empty"`  // share of searches with no result
	ByKind   map[string]float64 `json:"byKind"` // nDCG@10 per kind
	N        int                `json:"n"`
}

// Evaluate runs rank (query -> product handles, best first) over Queries.
func Evaluate(rank func(q string) []string, ndcg func([]string, map[string]int, int) float64, recall func([]string, map[string]int, int) float64) Score {
	s := Score{ByKind: map[string]float64{}}
	count := map[string]int{}
	for _, q := range Queries {
		r := rank(q.Q)
		n := ndcg(r, q.Rel, 10)
		s.NDCG10 += n
		s.Recall50 += recall(r, q.Rel, 50)
		if len(r) == 0 {
			s.Empty++
		}
		s.ByKind[q.Kind] += n
		count[q.Kind]++
		s.N++
	}
	s.NDCG10 /= float64(s.N)
	s.Recall50 /= float64(s.N)
	s.Empty /= float64(s.N)
	for k := range s.ByKind {
		s.ByKind[k] /= float64(count[k])
	}
	return s
}
