package notify

import (
	"context"
	"strings"
)

// Buyer emails in the store's other languages. A template the store did not
// change is sent with the copy below in the buyer's language; a template the
// store rewrote keeps the store's words, translated phrase by phrase with the
// store's own translations when it has them.

// Copy is a template's subject and blocks in one language.
type Copy struct {
	Subject string
	Blocks  Blocks
}

type fixed struct{ lang, downloads, unsubscribe, unsubscribeText string }

func fixedWords(lang string) fixed {
	switch lang {
	case "en":
		return fixed{"en", "Your downloads", "I don't want these emails anymore", "Unsubscribe"}
	case "fr":
		return fixed{"fr", "Vos téléchargements", "Je ne veux plus recevoir ces e-mails", "Se désabonner"}
	case "pt":
		return fixed{"pt", "Seus downloads", "Não quero mais receber estes e-mails", "Cancelar inscrição"}
	case "id":
		return fixed{"id", "Unduhan Anda", "Saya tidak ingin menerima email ini lagi", "Berhenti berlangganan"}
	case "ja":
		return fixed{"ja", "ダウンロード", "今後このメールを受け取らない", "配信停止"}
	}
	return fixed{"es", "Tus descargas", "Ya no quiero recibir estos correos", "Darse de baja"}
}

// CopyIn returns the built-in copy of a template in a language.
func CopyIn(lang, key string) (Copy, bool) {
	c, ok := copies[lang][key]
	return c, ok
}

var copies = map[string]map[string]Copy{
	"en": {
		TplCampaign: {"{{tienda.nombre}}", Blocks{"News", "", "Visit the store", "You're getting this email because you agreed to receive news from the store."}},
		TplOrderConfirmed: {"We've confirmed your order #{{pedido.numero}}", Blocks{"Thank you for your purchase, {{cliente.nombre}}!",
			"We received your payment and are already preparing your order #{{pedido.numero}}.\n\nWe'll email you when it leaves our warehouse.", "View my order", "If you have questions, reply to this email."}},
		TplOrderShipped: {"Your order #{{pedido.numero}} is on its way", Blocks{"Your order is on its way",
			"Hi {{cliente.nombre}}, we shipped your order #{{pedido.numero}} with {{guia.paqueteria}}.\n\nTracking number: {{guia.numero}}", "Track shipment", "Tracking may take a few hours to update."}},
		TplOrderDelivered: {"Your order #{{pedido.numero}} was delivered", Blocks{"Your order arrived",
			"Hi {{cliente.nombre}}, your order #{{pedido.numero}} was delivered.\n\nIf something isn't right, reply to this email and we'll fix it.", "View my order", ""}},
		TplRefundIssued: {"Refund for your order #{{pedido.numero}}", Blocks{"We issued a refund",
			"Hi {{cliente.nombre}}, we refunded {{reembolso.monto}} from your order #{{pedido.numero}}.\n\nYour bank may take 5 to 10 business days to show it.", "View my order", ""}},
		TplReturnReceived: {"We received your return {{devolucion.numero}}", Blocks{"We received your return",
			"Hi {{cliente.nombre}}, we received the products from your return {{devolucion.numero}} (order #{{pedido.numero}}).\n\n{{devolucion.resolucion}}", "View my order", ""}},
		TplAccessCode: {"Your code to sign in to {{tienda.nombre}}: {{codigo}}", Blocks{"Your access code",
			"Use this code to sign in to your account at {{tienda.nombre}}:\n\n{{codigo}}\n\nIt expires in 10 minutes. If you didn't ask for it, ignore this email.", "", "We'll never ask for this code by phone or chat."}},
		TplInvoiceReady: {"The invoice for your order #{{pedido.numero}} is ready", Blocks{"Your invoice is ready",
			"Hi {{cliente.nombre}}, you can now download the invoice for your order #{{pedido.numero}}.", "Download invoice", ""}},
		TplAbandonedCart: {"You left something in your cart", Blocks{"Your cart is waiting",
			"Hi {{cliente.nombre}}, we saved the products you chose at {{tienda.nombre}}.\n\nYou can finish your purchase whenever you like.", "Back to my cart", "You're getting this email because you started a purchase in our store."}},
		TplGiftCard: {"You have {{tarjeta.saldo}} to spend at {{tienda.nombre}}", Blocks{"{{cliente.nombre}}, you have credit at {{tienda.nombre}}",
			"Your code is:\n\n{{tarjeta.codigo}}\n\nBalance: {{tarjeta.saldo}}. Valid: {{tarjeta.vence}}.\n\nEnter it at checkout, under “Gift card or store credit”. If you don't use it all, the rest stays on the same code.", "Go to the store", "Keep this email: the code works like money in the store."}},
		TplSubscriptionCreated: {"Your subscription at {{tienda.nombre}} is active", Blocks{"{{cliente.nombre}}, your subscription is ready",
			"You'll receive {{suscripcion.productos}} {{suscripcion.frecuencia}}.\n\nNext delivery: {{suscripcion.proxima}}. We charge your saved card on the day of each delivery.\n\nYou can pause, skip a delivery or cancel anytime, without calling anyone.", "Manage my subscription", "Keep this email: the button is your access to the subscription."}},
		TplSubscriptionFailed: {"We couldn't charge your subscription at {{tienda.nombre}}", Blocks{"Your subscription is on hold",
			"Hi {{cliente.nombre}}, we tried to charge your {{suscripcion.frecuencia}} delivery and your card didn't go through.\n\nDon't worry: no charge was made. Open your subscription to reactivate or cancel it.", "Review my subscription", ""}},
		TplTeamInvite: {"You were invited to the {{tienda.nombre}} team", Blocks{"{{cliente.nombre}}, join the {{tienda.nombre}} team",
			"{{equipo.invita}} invited you to manage the store on Apexx.\n\nCreate your password with the button below. The invitation expires in 7 days and only works once.", "Accept invitation", "If you weren't expecting this invitation, ignore this email."}},
		TplPurchaseApproval: {"{{solicitud.quien}} asks to approve a purchase of {{solicitud.total}}", Blocks{"Purchase to approve at {{tienda.nombre}}",
			"{{solicitud.quien}} wants to buy on {{empresa.nombre}}'s credit for {{solicitud.total}}.\n\nReview the products and approve or reject. The order is only placed if you approve it.", "Review the purchase", "You're getting this email because you approve your company's purchases in this store."}},
		TplQuoteOffered: {"Your quote #{{cotizacion.numero}} is ready", Blocks{"We have your quote",
			"Hi {{cliente.nombre}}, we prepared quote #{{cotizacion.numero}} for {{cotizacion.total}}.\n\nYou can accept it, ask for changes or reject it. It's valid until {{cotizacion.vence}}.", "View the quote", "You're getting this email because you asked for a quote in this store."}},
		TplCompanyDue: {"{{empresa.nombre}}: {{saldo.monto}} is due on {{saldo.vence}}", Blocks{"Your balance is coming due",
			"{{empresa.nombre}}'s balance of {{saldo.monto}} is due on {{saldo.vence}}.\n\n{{saldo.cobro}}", "View my company", "You're getting this email because you manage your company's account in this store."}},
		TplSandboxExpiring: {"Your {{tienda.nombre}} sandbox will be deleted on {{sandbox.vence}}", Blocks{"Your sandbox hasn't been used in a while",
			"Nobody has entered the {{tienda.nombre}} sandbox in {{sandbox.dias}} days. It will be deleted on {{sandbox.vence}}.\n\nIf you still use it, enter it once from the panel and it stays. What you already published to production is not lost.", "Open the sandbox", "You're getting this email because you own the store on Apexx."}},
		TplCompanyAutopayFail: {"We couldn't charge {{empresa.nombre}}'s balance", Blocks{"Your card didn't go through",
			"We tried to charge {{saldo.monto}} of {{empresa.nombre}}'s overdue balance and the bank declined it.\n\n{{saldo.cobro}}", "Update the card", "You're getting this email because you manage your company's account in this store."}},
		TplPaymentLink: {"Complete the payment for your order at {{tienda.nombre}}", Blocks{"Your order is ready to pay",
			"Hi {{cliente.nombre}}, we prepared your order for {{pago.monto}}.\n\nPay securely with the button below. The link expires in 7 days.", "Pay now", ""}},
	},
	"fr": {
		TplCampaign: {"{{tienda.nombre}}", Blocks{"Nouveautés", "", "Voir la boutique", "Vous recevez cet e-mail car vous avez accepté les actualités de la boutique."}},
		TplOrderConfirmed: {"Nous confirmons votre commande n° {{pedido.numero}}", Blocks{"Merci pour votre achat, {{cliente.nombre}} !",
			"Nous avons reçu votre paiement et préparons déjà votre commande n° {{pedido.numero}}.\n\nNous vous écrirons quand elle quittera notre entrepôt.", "Voir ma commande", "Pour toute question, répondez à cet e-mail."}},
		TplOrderShipped: {"Votre commande n° {{pedido.numero}} est en route", Blocks{"Votre commande est en route",
			"Bonjour {{cliente.nombre}}, nous avons expédié votre commande n° {{pedido.numero}} avec {{guia.paqueteria}}.\n\nNuméro de suivi : {{guia.numero}}", "Suivre le colis", "Le suivi peut mettre quelques heures à se mettre à jour."}},
		TplOrderDelivered: {"Votre commande n° {{pedido.numero}} a été livrée", Blocks{"Votre commande est arrivée",
			"Bonjour {{cliente.nombre}}, votre commande n° {{pedido.numero}} a été livrée.\n\nSi quelque chose ne va pas, répondez à cet e-mail et nous le réglerons.", "Voir ma commande", ""}},
		TplRefundIssued: {"Remboursement de votre commande n° {{pedido.numero}}", Blocks{"Nous avons effectué un remboursement",
			"Bonjour {{cliente.nombre}}, nous avons remboursé {{reembolso.monto}} de votre commande n° {{pedido.numero}}.\n\nVotre banque peut mettre 5 à 10 jours ouvrés à l'afficher.", "Voir ma commande", ""}},
		TplReturnReceived: {"Nous avons reçu votre retour {{devolucion.numero}}", Blocks{"Nous avons reçu votre retour",
			"Bonjour {{cliente.nombre}}, nous avons reçu les produits de votre retour {{devolucion.numero}} (commande n° {{pedido.numero}}).\n\n{{devolucion.resolucion}}", "Voir ma commande", ""}},
		TplAccessCode: {"Votre code pour accéder à {{tienda.nombre}} : {{codigo}}", Blocks{"Votre code d'accès",
			"Utilisez ce code pour accéder à votre compte sur {{tienda.nombre}} :\n\n{{codigo}}\n\nIl expire dans 10 minutes. Si vous ne l'avez pas demandé, ignorez cet e-mail.", "", "Nous ne vous demanderons jamais ce code par téléphone ni par chat."}},
		TplInvoiceReady: {"La facture de votre commande n° {{pedido.numero}} est prête", Blocks{"Votre facture est prête",
			"Bonjour {{cliente.nombre}}, vous pouvez télécharger la facture de votre commande n° {{pedido.numero}}.", "Télécharger la facture", ""}},
		TplAbandonedCart: {"Vous avez oublié quelque chose dans votre panier", Blocks{"Votre panier vous attend",
			"Bonjour {{cliente.nombre}}, nous avons gardé les produits que vous avez choisis sur {{tienda.nombre}}.\n\nVous pouvez finaliser votre achat quand vous voulez.", "Retour à mon panier", "Vous recevez cet e-mail car vous avez commencé un achat dans notre boutique."}},
		TplGiftCard: {"Vous avez {{tarjeta.saldo}} à utiliser sur {{tienda.nombre}}", Blocks{"{{cliente.nombre}}, vous avez un avoir sur {{tienda.nombre}}",
			"Votre code est :\n\n{{tarjeta.codigo}}\n\nSolde : {{tarjeta.saldo}}. Validité : {{tarjeta.vence}}.\n\nSaisissez-le au paiement, dans « Carte cadeau ou avoir ». Si vous ne l'utilisez pas entièrement, le reste reste sur le même code.", "Aller à la boutique", "Conservez cet e-mail : le code vaut de l'argent dans la boutique."}},
		TplSubscriptionCreated: {"Votre abonnement sur {{tienda.nombre}} est actif", Blocks{"{{cliente.nombre}}, votre abonnement est prêt",
			"Vous recevrez {{suscripcion.productos}} {{suscripcion.frecuencia}}.\n\nProchaine livraison : {{suscripcion.proxima}}. Nous débitons votre carte enregistrée le jour de chaque livraison.\n\nVous pouvez mettre en pause, sauter une livraison ou annuler quand vous voulez, sans appeler personne.", "Gérer mon abonnement", "Conservez cet e-mail : le bouton est votre accès à l'abonnement."}},
		TplSubscriptionFailed: {"Nous n'avons pas pu débiter votre abonnement sur {{tienda.nombre}}", Blocks{"Votre abonnement est suspendu",
			"Bonjour {{cliente.nombre}}, nous avons tenté de débiter votre livraison {{suscripcion.frecuencia}} et votre carte a été refusée.\n\nPas d'inquiétude : aucun débit n'a été effectué. Ouvrez votre abonnement pour le réactiver ou l'annuler.", "Vérifier mon abonnement", ""}},
		TplTeamInvite: {"Vous êtes invité dans l'équipe de {{tienda.nombre}}", Blocks{"{{cliente.nombre}}, rejoignez l'équipe de {{tienda.nombre}}",
			"{{equipo.invita}} vous invite à gérer la boutique sur Apexx.\n\nCréez votre mot de passe avec le bouton ci-dessous. L'invitation expire dans 7 jours et ne fonctionne qu'une fois.", "Accepter l'invitation", "Si vous n'attendiez pas cette invitation, ignorez cet e-mail."}},
		TplPurchaseApproval: {"{{solicitud.quien}} demande d'approuver un achat de {{solicitud.total}}", Blocks{"Achat à approuver sur {{tienda.nombre}}",
			"{{solicitud.quien}} souhaite acheter à crédit au nom de {{empresa.nombre}} pour {{solicitud.total}}.\n\nVérifiez les produits et approuvez ou refusez. La commande n'est passée que si vous l'approuvez.", "Vérifier l'achat", "Vous recevez cet e-mail car vous approuvez les achats de votre entreprise dans cette boutique."}},
		TplQuoteOffered: {"Votre devis n° {{cotizacion.numero}} est prêt", Blocks{"Nous avons votre devis",
			"Bonjour {{cliente.nombre}}, nous avons préparé le devis n° {{cotizacion.numero}} pour {{cotizacion.total}}.\n\nVous pouvez l'accepter, demander des modifications ou le refuser. Il est valable jusqu'au {{cotizacion.vence}}.", "Voir le devis", "Vous recevez cet e-mail car vous avez demandé un devis dans cette boutique."}},
		TplCompanyDue: {"{{empresa.nombre}} : {{saldo.monto}} à régler le {{saldo.vence}}", Blocks{"Votre solde arrive à échéance",
			"Le solde de {{empresa.nombre}} de {{saldo.monto}} est à régler le {{saldo.vence}}.\n\n{{saldo.cobro}}", "Voir mon entreprise", "Vous recevez cet e-mail car vous gérez le compte de votre entreprise dans cette boutique."}},
		TplSandboxExpiring: {"Le sandbox de {{tienda.nombre}} sera supprimé le {{sandbox.vence}}", Blocks{"Votre sandbox n'a pas servi depuis un moment",
			"Personne n'est entré dans le sandbox de {{tienda.nombre}} depuis {{sandbox.dias}} jours. Il sera supprimé le {{sandbox.vence}}.\n\nSi vous l'utilisez encore, entrez-y une fois depuis le panneau et il est conservé. Ce que vous avez déjà publié en production n'est pas perdu.", "Ouvrir le sandbox", "Vous recevez cet e-mail parce que vous êtes propriétaire de la boutique sur Apexx."}},
		TplCompanyAutopayFail: {"Nous n'avons pas pu débiter le solde de {{empresa.nombre}}", Blocks{"Votre carte a été refusée",
			"Nous avons tenté de débiter {{saldo.monto}} du solde échu de {{empresa.nombre}} et la banque l'a refusé.\n\n{{saldo.cobro}}", "Mettre à jour la carte", "Vous recevez cet e-mail car vous gérez le compte de votre entreprise dans cette boutique."}},
		TplPaymentLink: {"Finalisez le paiement de votre commande sur {{tienda.nombre}}", Blocks{"Votre commande est prête à être payée",
			"Bonjour {{cliente.nombre}}, nous avons préparé votre commande pour {{pago.monto}}.\n\nPayez en toute sécurité avec le bouton ci-dessous. Le lien expire dans 7 jours.", "Payer maintenant", ""}},
	},
	"pt": {
		TplCampaign: {"{{tienda.nombre}}", Blocks{"Novidades", "", "Ver a loja", "Você recebe este e-mail porque aceitou novidades da loja."}},
		TplOrderConfirmed: {"Confirmamos seu pedido #{{pedido.numero}}", Blocks{"Obrigado pela sua compra, {{cliente.nombre}}!",
			"Recebemos seu pagamento e já estamos preparando seu pedido #{{pedido.numero}}.\n\nAvisaremos por e-mail quando sair do nosso armazém.", "Ver meu pedido", "Se tiver dúvidas, responda a este e-mail."}},
		TplOrderShipped: {"Seu pedido #{{pedido.numero}} está a caminho", Blocks{"Seu pedido está a caminho",
			"Olá {{cliente.nombre}}, enviamos seu pedido #{{pedido.numero}} pela {{guia.paqueteria}}.\n\nCódigo de rastreio: {{guia.numero}}", "Rastrear envio", "O rastreio pode levar algumas horas para atualizar."}},
		TplOrderDelivered: {"Seu pedido #{{pedido.numero}} foi entregue", Blocks{"Seu pedido chegou",
			"Olá {{cliente.nombre}}, seu pedido #{{pedido.numero}} foi entregue.\n\nSe algo não estiver certo, responda a este e-mail e resolvemos.", "Ver meu pedido", ""}},
		TplRefundIssued: {"Reembolso do seu pedido #{{pedido.numero}}", Blocks{"Fizemos um reembolso",
			"Olá {{cliente.nombre}}, reembolsamos {{reembolso.monto}} do seu pedido #{{pedido.numero}}.\n\nSeu banco pode levar de 5 a 10 dias úteis para mostrar.", "Ver meu pedido", ""}},
		TplReturnReceived: {"Recebemos sua devolução {{devolucion.numero}}", Blocks{"Recebemos sua devolução",
			"Olá {{cliente.nombre}}, recebemos os produtos da sua devolução {{devolucion.numero}} (pedido #{{pedido.numero}}).\n\n{{devolucion.resolucion}}", "Ver meu pedido", ""}},
		TplAccessCode: {"Seu código para entrar em {{tienda.nombre}}: {{codigo}}", Blocks{"Seu código de acesso",
			"Use este código para entrar na sua conta em {{tienda.nombre}}:\n\n{{codigo}}\n\nVence em 10 minutos. Se você não pediu, ignore este e-mail.", "", "Nunca pediremos este código por telefone nem por chat."}},
		TplInvoiceReady: {"A fatura do seu pedido #{{pedido.numero}} está pronta", Blocks{"Sua fatura está pronta",
			"Olá {{cliente.nombre}}, você já pode baixar a fatura do seu pedido #{{pedido.numero}}.", "Baixar fatura", ""}},
		TplAbandonedCart: {"Você deixou algo no seu carrinho", Blocks{"Seu carrinho espera por você",
			"Olá {{cliente.nombre}}, guardamos os produtos que você escolheu em {{tienda.nombre}}.\n\nVocê pode terminar sua compra quando quiser.", "Voltar ao meu carrinho", "Você recebe este e-mail porque começou uma compra na nossa loja."}},
		TplGiftCard: {"Você tem {{tarjeta.saldo}} para usar em {{tienda.nombre}}", Blocks{"{{cliente.nombre}}, você tem saldo em {{tienda.nombre}}",
			"Seu código é:\n\n{{tarjeta.codigo}}\n\nSaldo: {{tarjeta.saldo}}. Validade: {{tarjeta.vence}}.\n\nDigite no pagamento, em “Cartão-presente ou crédito”. Se não usar tudo, o restante fica no mesmo código.", "Ir para a loja", "Guarde este e-mail: o código vale como dinheiro na loja."}},
		TplSubscriptionCreated: {"Sua assinatura em {{tienda.nombre}} está ativa", Blocks{"{{cliente.nombre}}, sua assinatura está pronta",
			"Você receberá {{suscripcion.productos}} {{suscripcion.frecuencia}}.\n\nPróxima entrega: {{suscripcion.proxima}}. Cobramos no seu cartão salvo no dia de cada entrega.\n\nVocê pode pausar, pular uma entrega ou cancelar quando quiser, sem ligar para ninguém.", "Gerenciar minha assinatura", "Guarde este e-mail: o botão é seu acesso à assinatura."}},
		TplSubscriptionFailed: {"Não conseguimos cobrar sua assinatura em {{tienda.nombre}}", Blocks{"Sua assinatura está parada",
			"Olá {{cliente.nombre}}, tentamos cobrar sua entrega {{suscripcion.frecuencia}} e seu cartão não passou.\n\nNão se preocupe: nenhuma cobrança foi feita. Entre na sua assinatura para reativá-la ou cancelá-la.", "Revisar minha assinatura", ""}},
		TplTeamInvite: {"Você foi convidado para a equipe de {{tienda.nombre}}", Blocks{"{{cliente.nombre}}, junte-se à equipe de {{tienda.nombre}}",
			"{{equipo.invita}} convidou você para administrar a loja na Apexx.\n\nCrie sua senha com o botão abaixo. O convite vence em 7 dias e funciona só uma vez.", "Aceitar convite", "Se você não esperava este convite, ignore este e-mail."}},
		TplPurchaseApproval: {"{{solicitud.quien}} pede aprovação de uma compra de {{solicitud.total}}", Blocks{"Compra a aprovar em {{tienda.nombre}}",
			"{{solicitud.quien}} quer comprar a crédito de {{empresa.nombre}} por {{solicitud.total}}.\n\nRevise os produtos e aprove ou rejeite. O pedido só é feito se você aprovar.", "Revisar a compra", "Você recebe este e-mail porque aprova as compras da sua empresa nesta loja."}},
		TplQuoteOffered: {"Sua cotação #{{cotizacion.numero}} está pronta", Blocks{"Temos sua cotação",
			"Olá {{cliente.nombre}}, preparamos a cotação #{{cotizacion.numero}} por {{cotizacion.total}}.\n\nVocê pode aceitá-la, pedir alterações ou rejeitá-la. Vale até {{cotizacion.vence}}.", "Ver a cotação", "Você recebe este e-mail porque pediu uma cotação nesta loja."}},
		TplCompanyDue: {"{{empresa.nombre}}: {{saldo.monto}} vence em {{saldo.vence}}", Blocks{"Seu saldo está para vencer",
			"O saldo de {{empresa.nombre}} de {{saldo.monto}} vence em {{saldo.vence}}.\n\n{{saldo.cobro}}", "Ver minha empresa", "Você recebe este e-mail porque administra a conta da sua empresa nesta loja."}},
		TplSandboxExpiring: {"O sandbox de {{tienda.nombre}} será apagado em {{sandbox.vence}}", Blocks{"Seu sandbox está há tempo sem uso",
			"Ninguém entrou no sandbox de {{tienda.nombre}} em {{sandbox.dias}} dias. Ele será apagado em {{sandbox.vence}}.\n\nSe você ainda o usa, entre uma vez pelo painel e ele é mantido. O que você já publicou em produção não se perde.", "Abrir o sandbox", "Você recebe este e-mail porque é dono da loja na Apexx."}},
		TplCompanyAutopayFail: {"Não conseguimos cobrar o saldo de {{empresa.nombre}}", Blocks{"Seu cartão não passou",
			"Tentamos cobrar {{saldo.monto}} do saldo vencido de {{empresa.nombre}} e o banco recusou.\n\n{{saldo.cobro}}", "Atualizar o cartão", "Você recebe este e-mail porque administra a conta da sua empresa nesta loja."}},
		TplPaymentLink: {"Conclua o pagamento do seu pedido em {{tienda.nombre}}", Blocks{"Seu pedido está pronto para pagar",
			"Olá {{cliente.nombre}}, preparamos seu pedido de {{pago.monto}}.\n\nPague com segurança pelo botão abaixo. O link vence em 7 dias.", "Pagar agora", ""}},
	},
	"id": {
		TplCampaign: {"{{tienda.nombre}}", Blocks{"Kabar terbaru", "", "Kunjungi toko", "Anda menerima email ini karena setuju menerima kabar dari toko."}},
		TplOrderConfirmed: {"Pesanan #{{pedido.numero}} Anda sudah dikonfirmasi", Blocks{"Terima kasih atas pembelian Anda, {{cliente.nombre}}!",
			"Kami menerima pembayaran Anda dan sedang menyiapkan pesanan #{{pedido.numero}}.\n\nKami akan mengabari lewat email saat pesanan keluar dari gudang.", "Lihat pesanan saya", "Jika ada pertanyaan, balas email ini."}},
		TplOrderShipped: {"Pesanan #{{pedido.numero}} Anda sedang dikirim", Blocks{"Pesanan Anda sedang dikirim",
			"Halo {{cliente.nombre}}, kami mengirim pesanan #{{pedido.numero}} Anda melalui {{guia.paqueteria}}.\n\nNomor resi: {{guia.numero}}", "Lacak pengiriman", "Pelacakan mungkin perlu beberapa jam untuk diperbarui."}},
		TplOrderDelivered: {"Pesanan #{{pedido.numero}} Anda sudah diterima", Blocks{"Pesanan Anda sudah tiba",
			"Halo {{cliente.nombre}}, pesanan #{{pedido.numero}} Anda sudah diterima.\n\nJika ada yang tidak sesuai, balas email ini dan kami akan menyelesaikannya.", "Lihat pesanan saya", ""}},
		TplRefundIssued: {"Pengembalian dana pesanan #{{pedido.numero}} Anda", Blocks{"Kami telah mengembalikan dana",
			"Halo {{cliente.nombre}}, kami mengembalikan {{reembolso.monto}} dari pesanan #{{pedido.numero}} Anda.\n\nBank Anda mungkin perlu 5 sampai 10 hari kerja untuk menampilkannya.", "Lihat pesanan saya", ""}},
		TplReturnReceived: {"Kami menerima pengembalian {{devolucion.numero}} Anda", Blocks{"Kami menerima pengembalian Anda",
			"Halo {{cliente.nombre}}, kami menerima produk dari pengembalian {{devolucion.numero}} Anda (pesanan #{{pedido.numero}}).\n\n{{devolucion.resolucion}}", "Lihat pesanan saya", ""}},
		TplAccessCode: {"Kode Anda untuk masuk ke {{tienda.nombre}}: {{codigo}}", Blocks{"Kode akses Anda",
			"Gunakan kode ini untuk masuk ke akun Anda di {{tienda.nombre}}:\n\n{{codigo}}\n\nKedaluwarsa dalam 10 menit. Jika Anda tidak memintanya, abaikan email ini.", "", "Kami tidak akan pernah meminta kode ini lewat telepon atau chat."}},
		TplInvoiceReady: {"Faktur pesanan #{{pedido.numero}} Anda sudah siap", Blocks{"Faktur Anda sudah siap",
			"Halo {{cliente.nombre}}, Anda sudah bisa mengunduh faktur pesanan #{{pedido.numero}}.", "Unduh faktur", ""}},
		TplAbandonedCart: {"Ada yang tertinggal di keranjang Anda", Blocks{"Keranjang Anda menunggu",
			"Halo {{cliente.nombre}}, kami menyimpan produk yang Anda pilih di {{tienda.nombre}}.\n\nAnda bisa menyelesaikan pembelian kapan saja.", "Kembali ke keranjang", "Anda menerima email ini karena memulai pembelian di toko kami."}},
		TplGiftCard: {"Anda punya {{tarjeta.saldo}} untuk dipakai di {{tienda.nombre}}", Blocks{"{{cliente.nombre}}, Anda punya saldo di {{tienda.nombre}}",
			"Kode Anda:\n\n{{tarjeta.codigo}}\n\nSaldo: {{tarjeta.saldo}}. Berlaku: {{tarjeta.vence}}.\n\nMasukkan saat pembayaran, di “Kartu hadiah atau saldo toko”. Jika tidak terpakai semua, sisanya tetap di kode yang sama.", "Ke toko", "Simpan email ini: kode ini berlaku seperti uang di toko."}},
		TplSubscriptionCreated: {"Langganan Anda di {{tienda.nombre}} sudah aktif", Blocks{"{{cliente.nombre}}, langganan Anda sudah siap",
			"Anda akan menerima {{suscripcion.productos}} {{suscripcion.frecuencia}}.\n\nPengiriman berikutnya: {{suscripcion.proxima}}. Kami menagih kartu tersimpan Anda pada hari setiap pengiriman.\n\nAnda bisa menjeda, melewati pengiriman, atau membatalkan kapan saja tanpa menelepon siapa pun.", "Kelola langganan saya", "Simpan email ini: tombolnya adalah akses ke langganan Anda."}},
		TplSubscriptionFailed: {"Kami tidak dapat menagih langganan Anda di {{tienda.nombre}}", Blocks{"Langganan Anda dihentikan",
			"Halo {{cliente.nombre}}, kami mencoba menagih pengiriman {{suscripcion.frecuencia}} Anda dan kartu Anda ditolak.\n\nJangan khawatir: tidak ada tagihan yang dibuat. Buka langganan Anda untuk mengaktifkan kembali atau membatalkannya.", "Tinjau langganan saya", ""}},
		TplTeamInvite: {"Anda diundang ke tim {{tienda.nombre}}", Blocks{"{{cliente.nombre}}, bergabunglah dengan tim {{tienda.nombre}}",
			"{{equipo.invita}} mengundang Anda untuk mengelola toko di Apexx.\n\nBuat kata sandi dengan tombol di bawah. Undangan kedaluwarsa dalam 7 hari dan hanya berlaku sekali.", "Terima undangan", "Jika Anda tidak menunggu undangan ini, abaikan email ini."}},
		TplPurchaseApproval: {"{{solicitud.quien}} meminta persetujuan pembelian {{solicitud.total}}", Blocks{"Pembelian perlu disetujui di {{tienda.nombre}}",
			"{{solicitud.quien}} ingin membeli secara kredit atas nama {{empresa.nombre}} sebesar {{solicitud.total}}.\n\nTinjau produknya lalu setujui atau tolak. Pesanan hanya dibuat jika Anda menyetujuinya.", "Tinjau pembelian", "Anda menerima email ini karena menyetujui pembelian perusahaan Anda di toko ini."}},
		TplQuoteOffered: {"Penawaran #{{cotizacion.numero}} Anda sudah siap", Blocks{"Penawaran Anda sudah siap",
			"Halo {{cliente.nombre}}, kami menyiapkan penawaran #{{cotizacion.numero}} sebesar {{cotizacion.total}}.\n\nAnda bisa menerima, meminta perubahan, atau menolaknya. Berlaku hingga {{cotizacion.vence}}.", "Lihat penawaran", "Anda menerima email ini karena meminta penawaran di toko ini."}},
		TplCompanyDue: {"{{empresa.nombre}}: {{saldo.monto}} jatuh tempo {{saldo.vence}}", Blocks{"Saldo Anda akan jatuh tempo",
			"Saldo {{empresa.nombre}} sebesar {{saldo.monto}} jatuh tempo pada {{saldo.vence}}.\n\n{{saldo.cobro}}", "Lihat perusahaan saya", "Anda menerima email ini karena mengelola akun perusahaan Anda di toko ini."}},
		TplSandboxExpiring: {"Sandbox {{tienda.nombre}} akan dihapus pada {{sandbox.vence}}", Blocks{"Sandbox Anda sudah lama tidak dipakai",
			"Tidak ada yang masuk ke sandbox {{tienda.nombre}} selama {{sandbox.dias}} hari. Sandbox akan dihapus pada {{sandbox.vence}}.\n\nJika masih Anda pakai, masuk sekali dari panel dan sandbox tetap disimpan. Yang sudah Anda terbitkan ke produksi tidak hilang.", "Buka sandbox", "Anda menerima email ini karena Anda pemilik toko di Apexx."}},
		TplCompanyAutopayFail: {"Kami tidak dapat menagih saldo {{empresa.nombre}}", Blocks{"Kartu Anda ditolak",
			"Kami mencoba menagih {{saldo.monto}} dari saldo jatuh tempo {{empresa.nombre}} dan bank menolaknya.\n\n{{saldo.cobro}}", "Perbarui kartu", "Anda menerima email ini karena mengelola akun perusahaan Anda di toko ini."}},
		TplPaymentLink: {"Selesaikan pembayaran pesanan Anda di {{tienda.nombre}}", Blocks{"Pesanan Anda siap dibayar",
			"Halo {{cliente.nombre}}, kami menyiapkan pesanan Anda sebesar {{pago.monto}}.\n\nBayar dengan aman lewat tombol di bawah. Tautan kedaluwarsa dalam 7 hari.", "Bayar sekarang", ""}},
	},
	"ja": {
		TplCampaign: {"{{tienda.nombre}}", Blocks{"お知らせ", "", "ストアを見る", "ストアからのお知らせの受信に同意されたため、このメールをお送りしています。"}},
		TplOrderConfirmed: {"ご注文 #{{pedido.numero}} を承りました", Blocks{"{{cliente.nombre}} 様、ご購入ありがとうございます！",
			"お支払いを確認し、ご注文 #{{pedido.numero}} の準備を始めました。\n\n倉庫から発送したらメールでお知らせします。", "注文を確認する", "ご不明な点はこのメールにご返信ください。"}},
		TplOrderShipped: {"ご注文 #{{pedido.numero}} を発送しました", Blocks{"ご注文を発送しました",
			"{{cliente.nombre}} 様、ご注文 #{{pedido.numero}} を {{guia.paqueteria}} で発送しました。\n\n追跡番号：{{guia.numero}}", "配送状況を確認", "追跡情報の反映には数時間かかる場合があります。"}},
		TplOrderDelivered: {"ご注文 #{{pedido.numero}} をお届けしました", Blocks{"ご注文が届きました",
			"{{cliente.nombre}} 様、ご注文 #{{pedido.numero}} をお届けしました。\n\n不備がございましたら、このメールにご返信ください。対応いたします。", "注文を確認する", ""}},
		TplRefundIssued: {"ご注文 #{{pedido.numero}} の返金", Blocks{"返金しました",
			"{{cliente.nombre}} 様、ご注文 #{{pedido.numero}} から {{reembolso.monto}} を返金しました。\n\n銀行への反映には5〜10営業日かかる場合があります。", "注文を確認する", ""}},
		TplReturnReceived: {"返品 {{devolucion.numero}} を受け取りました", Blocks{"返品を受け取りました",
			"{{cliente.nombre}} 様、返品 {{devolucion.numero}}（ご注文 #{{pedido.numero}}）の商品を受け取りました。\n\n{{devolucion.resolucion}}", "注文を確認する", ""}},
		TplAccessCode: {"{{tienda.nombre}} へのログインコード：{{codigo}}", Blocks{"ログインコード",
			"{{tienda.nombre}} のアカウントにログインするには、このコードを使ってください：\n\n{{codigo}}\n\n有効期限は10分です。心当たりがない場合は、このメールを無視してください。", "", "このコードを電話やチャットでお尋ねすることはありません。"}},
		TplInvoiceReady: {"ご注文 #{{pedido.numero}} の請求書の準備ができました", Blocks{"請求書の準備ができました",
			"{{cliente.nombre}} 様、ご注文 #{{pedido.numero}} の請求書をダウンロードできます。", "請求書をダウンロード", ""}},
		TplAbandonedCart: {"カートに商品が残っています", Blocks{"カートでお待ちしています",
			"{{cliente.nombre}} 様、{{tienda.nombre}} でお選びいただいた商品を保存しています。\n\nいつでもご購入を完了できます。", "カートに戻る", "当ストアでご購入を開始されたため、このメールをお送りしています。"}},
		TplGiftCard: {"{{tienda.nombre}} で使える {{tarjeta.saldo}} があります", Blocks{"{{cliente.nombre}} 様、{{tienda.nombre}} の残高があります",
			"コード：\n\n{{tarjeta.codigo}}\n\n残高：{{tarjeta.saldo}}。有効期限：{{tarjeta.vence}}。\n\nお支払い時に「ギフトカードまたはストアクレジット」に入力してください。使い切らなかった分は同じコードに残ります。", "ストアへ", "このメールは保管してください。コードはストアで現金と同じように使えます。"}},
		TplSubscriptionCreated: {"{{tienda.nombre}} の定期購入が開始されました", Blocks{"{{cliente.nombre}} 様、定期購入の準備ができました",
			"{{suscripcion.productos}} を {{suscripcion.frecuencia}} お届けします。\n\n次回のお届け：{{suscripcion.proxima}}。お届け日ごとに保存済みのカードに請求します。\n\n一時停止、スキップ、解約はいつでも、電話なしで行えます。", "定期購入を管理", "このメールは保管してください。ボタンから定期購入にアクセスできます。"}},
		TplSubscriptionFailed: {"{{tienda.nombre}} の定期購入の請求ができませんでした", Blocks{"定期購入を停止しています",
			"{{cliente.nombre}} 様、{{suscripcion.frecuencia}} のお届け分を請求しようとしましたが、カードが承認されませんでした。\n\nご安心ください。請求は発生していません。定期購入を開いて再開または解約してください。", "定期購入を確認", ""}},
		TplTeamInvite: {"{{tienda.nombre}} のチームに招待されました", Blocks{"{{cliente.nombre}} 様、{{tienda.nombre}} のチームに参加しましょう",
			"{{equipo.invita}} さんから Apexx でのストア管理に招待されました。\n\n下のボタンからパスワードを作成してください。招待の有効期限は7日間で、1回のみ使えます。", "招待を承諾", "心当たりがない場合は、このメールを無視してください。"}},
		TplPurchaseApproval: {"{{solicitud.quien}} さんが {{solicitud.total}} の購入承認を依頼しています", Blocks{"{{tienda.nombre}} の承認待ちの購入",
			"{{solicitud.quien}} さんが {{empresa.nombre}} の与信で {{solicitud.total}} の購入を希望しています。\n\n商品を確認して承認または却下してください。承認された場合のみ注文されます。", "購入を確認", "このストアで貴社の購入を承認する担当者のため、このメールをお送りしています。"}},
		TplQuoteOffered: {"見積もり #{{cotizacion.numero}} の準備ができました", Blocks{"お見積もりをご用意しました",
			"{{cliente.nombre}} 様、見積もり #{{cotizacion.numero}}（{{cotizacion.total}}）をご用意しました。\n\n承諾、変更依頼、辞退のいずれかをお選びください。有効期限は {{cotizacion.vence}} です。", "見積もりを見る", "このストアで見積もりをご依頼いただいたため、このメールをお送りしています。"}},
		TplCompanyDue: {"{{empresa.nombre}}：{{saldo.monto}} の支払期限は {{saldo.vence}} です", Blocks{"残高の支払期限が近づいています",
			"{{empresa.nombre}} の残高 {{saldo.monto}} の支払期限は {{saldo.vence}} です。\n\n{{saldo.cobro}}", "会社のページを見る", "このストアで貴社のアカウントを管理しているため、このメールをお送りしています。"}},
		TplSandboxExpiring: {"{{tienda.nombre}} のサンドボックスは {{sandbox.vence}} に削除されます", Blocks{"サンドボックスがしばらく使われていません",
			"{{tienda.nombre}} のサンドボックスには {{sandbox.dias}} 日間誰も入っていません。{{sandbox.vence}} に削除されます。\n\nまだ使う場合は、管理画面から一度入れば保持されます。本番に公開した内容は失われません。", "サンドボックスを開く", "このメールは、Apexx のストアのオーナーであるため送信されています。"}},
		TplCompanyAutopayFail: {"{{empresa.nombre}} の残高を請求できませんでした", Blocks{"カードが承認されませんでした",
			"{{empresa.nombre}} の期限超過残高から {{saldo.monto}} を請求しようとしましたが、銀行に拒否されました。\n\n{{saldo.cobro}}", "カードを更新", "このストアで貴社のアカウントを管理しているため、このメールをお送りしています。"}},
		TplPaymentLink: {"{{tienda.nombre}} でのご注文のお支払いを完了してください", Blocks{"ご注文のお支払いの準備ができました",
			"{{cliente.nombre}} 様、{{pago.monto}} のご注文をご用意しました。\n\n下のボタンから安全にお支払いください。リンクの有効期限は7日間です。", "今すぐ支払う", ""}},
	},
}

// localize puts a message in the buyer's language: the built-in copy when the
// store kept the default text, the store's own words otherwise (phrase by
// phrase, with its translations), and the fixed words the order brings
// (totals labels, standard sentences).
func (s *Service) localize(tenant, lang string, tpl *Template, d *Data) {
	if lang == "" || lang == "es" {
		return
	}
	var phrase func(string) string
	if s.Phrases != nil {
		phrase = s.Phrases(tenant, lang)
	}
	if phrase == nil {
		phrase = func(x string) string { return x }
	}
	def, isBuiltin := DefinitionOf(tpl.Key)
	if c, ok := CopyIn(lang, tpl.Key); ok && isBuiltin && d.Override == nil && tpl.Subject == def.Subject && tpl.Blocks == def.Blocks {
		tpl.Subject, tpl.Blocks = c.Subject, c.Blocks
	} else {
		tpl.Subject = phrase(tpl.Subject)
		tpl.Blocks.Heading, tpl.Blocks.CTA, tpl.Blocks.Footer = phrase(tpl.Blocks.Heading), phrase(tpl.Blocks.CTA), phrase(tpl.Blocks.Footer)
		paras := splitParagraphs(tpl.Blocks.Body)
		for i := range paras {
			paras[i] = phrase(paras[i])
		}
		tpl.Blocks.Body = joinParagraphs(paras)
	}
	for i := range d.Totals {
		d.Totals[i].Label = phrase(d.Totals[i].Label)
	}
	vars := make(map[string]string, len(d.Vars))
	for k, v := range d.Vars {
		vars[k] = phrase(v)
	}
	d.Vars = vars
}

func splitParagraphs(body string) []string {
	return strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n\n")
}

func joinParagraphs(p []string) string { return strings.Join(p, "\n\n") }

type langKey struct{}

// WithLang marks a request's context with the buyer's language, so the emails
// it queues go out in that language.
func WithLang(ctx context.Context, lang string) context.Context {
	return context.WithValue(ctx, langKey{}, lang)
}

// LangFrom is the buyer language a request carries (see WithLang).
func LangFrom(ctx context.Context) string { return langFrom(ctx) }

func langFrom(ctx context.Context) string {
	l, _ := ctx.Value(langKey{}).(string)
	return l
}
