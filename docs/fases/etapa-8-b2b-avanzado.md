# Etapa 8 — B2B avanzado (noviembre 2026)

Estado: **hecha**. Cada función tiene una prueba de integración, un recorrido en el navegador (panel en escritorio, portal de la empresa en teléfono) y una medición de velocidad.

| Función | Dónde | Prueba |
|---|---|---|
| Empresas matriz y filiales (hasta 3 niveles) con crédito compartido y condiciones heredadas | Empresas › Editar › Empresa matriz; Mi empresa › Filiales | `TestCompanyHierarchy` |
| Roles por empresa: compra a crédito, ve el crédito, cotiza, aprueba, administra | Empresa › Roles; Mi empresa | `TestCompanyRoles` |
| Representante de ventas que compra por el cliente, con su permiso y todo registrado | Empresa › Quién atiende; tienda con aviso «comprando como…» | `TestSalesRepActsAsBuyer` |
| Acuerdos de compra: precio negociado por producto, mínimos, máximos y vigencia | Empresa › Acuerdos; Mi empresa › Pedir por acuerdo | `TestQuoteTemplates` |
| Cotización en PDF y comentarios por renglón (tienda y comprador) | Cotizaciones; página de la cotización | `TestQuotePDFAndLineComments` |
| Facturas de la empresa: pagar varias en línea, pagos que saldan las más antiguas, recibo en PDF | Empresa › Facturas; Mi empresa › Facturas | `TestCompanyInvoicesOnline` |
| Medios de pago permitidos por empresa y por sucursal | Empresa › Editar; pago | `TestCompanyPaymentMethods` |
| Documentos de la empresa cifrados (constancia, contrato) e importación de empresas por CSV con revisión previa | Empresa › Documentos; Empresas › Importar CSV | `TestCompanyFilesAndImport` |
| Punchout cXML (Ariba, Coupa, Jaggaer…): inicio, carrito de regreso y orden de compra | Empresa › Punchout (cXML) | `TestPunchout` |

## Decisiones de diseño y seguridad

- **Crédito compartido.** Toda compra a crédito bloquea la fila de la matriz del grupo, así que dos filiales no pueden gastar el mismo saldo al mismo tiempo. Una empresa no puede ser su propia matriz ni pasar de 3 niveles.
- **Roles.** El servidor los revisa en cada compra, cotización y aprobación; la tienda nunca confía en lo que muestra la pantalla. Siempre queda al menos una persona que administra la empresa.
- **Representantes.** Solo entran si la empresa lo permite («Mi empresa» › permiso). Usan un enlace de un solo uso, su nombre queda en el pedido, en una nota interna y en la bitácora con cadena de hashes, y la sesión tiene su propio límite de tiempo.
- **Acuerdos.** El precio acordado se aplica en el servidor; un pedido fuera de mínimos, máximos o vigencia se rechaza con el motivo (409 `agreement_rule`).
- **PDF propio.** Las cotizaciones y los recibos se arman con un escritor de PDF propio, sin dependencias, con acentos (WinAnsi) y títulos en UTF-16.
- **Facturas.** Pagar es idempotente por llave: repetir el pago nunca cobra dos veces y una tarjeta rechazada no cambia nada. Una transferencia sin factura salda primero las más antiguas. El libro contable queda cuadrado.
- **Documentos.** Se guardan cifrados por tienda; se revisa el tipo real del archivo (no la extensión) y hay un tope de tamaño.
- **Importación.** La revisión previa aplica las mismas validaciones que guardar. Cada línea se aplica por separado, así que una línea mala no detiene las demás.
- **Punchout.** De la clave compartida solo se guarda su hash, y se muestra una sola vez. El enlace de inicio sirve una vez y vence en una hora. El carrito regresa desde una página con su propia política de seguridad, que solo permite enviar al sistema de compras de la empresa. La dirección de regreso debe ser https. La orden de compra se acepta una sola vez por número de OC. Si un precio cambió desde el carrito, se rechaza en vez de cobrar otro precio. Los documentos cXML tienen tope de 1 MB y no aceptan entidades externas.

## Defectos que encontraron las auditorías (todos corregidos)

- Matriz inexistente: la consulta del grupo devolvía nulo; ahora toma «sin crédito compartido».
- El número de roles no se actualizaba al guardar un rol.
- La revisión del CSV no reconocía archivos guardados por Excel con marca BOM.
- Límites de peticiones compartidos entre pruebas causaban 429 intermitentes: ahora se reinician en cada prueba.
- Punchout: la política de seguridad de la tienda (`form-action 'self'`) impedía regresar el carrito al sistema de compras. Ahora se usa una página de regreso propia, con su política limitada a ese sistema.

## Velocidad medida (servidor de desarrollo)

- 41 rutas de la etapa (empresas, roles, compradores, facturas, documentos, punchout, cotizaciones y sus PDF): mediana de medianas **2.5 ms**, peor p95 **5.9 ms**, sin errores.

## Lo que queda fuera de este entorno

- Certificar el punchout con Ariba, Coupa o Jaggaer reales. Aquí se probó con documentos cXML 1.2 del estándar y un sistema de compras simulado.
