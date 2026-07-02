import { type ReactNode } from "react";
import { ArrowLeft, ArrowUpRight } from "lucide-react";
import { RELEASES_URL } from "../lib/content";
import { Button, Eyebrow } from "./ui";
import { Footer } from "./Footer";
import logo from "../assets/logo.jpg";

/* ── Content model ──────────────────────────────────────────────────────────
   The public thesis, transcribed as a content module rendered at build time
   (SSG) so the shipped HTML carries the full text — crawlable, not an empty
   shell. Paragraph strings keep their **bold** / *italic* markers from the
   source markdown; <Inline> renders them as semantic <strong>/<em>. */

type Block =
  | { k: "h2"; t: string }
  | { k: "p"; t: string }
  | { k: "lead"; t: string }
  | { k: "principles"; items: { n: number; t: string; b: string }[] }
  | { k: "doctrine"; t: string }
  | { k: "sources"; h: string; intro: string; items: string[] };

interface Paper {
  lang: "en" | "fr";
  kicker: string;
  title: string;
  subtitle: string;
  back: string;
  otherHref: string;
  blocks: Block[];
}

/** Minimal inline renderer for **bold** and *italic*, recursive so an italic
 *  work title nested inside a bold source lead renders correctly. */
function inlineNodes(text: string, key = ""): ReactNode[] {
  const nodes: ReactNode[] = [];
  const re = /\*\*([\s\S]+?)\*\*|\*([\s\S]+?)\*/g;
  let last = 0;
  let i = 0;
  let m: RegExpExecArray | null;
  while ((m = re.exec(text)) !== null) {
    if (m.index > last) nodes.push(text.slice(last, m.index));
    if (m[1] !== undefined) {
      nodes.push(<strong key={key + i}>{inlineNodes(m[1], key + i + "-")}</strong>);
    } else {
      nodes.push(<em key={key + i}>{inlineNodes(m[2], key + i + "-")}</em>);
    }
    i += 1;
    last = re.lastIndex;
  }
  if (last < text.length) nodes.push(text.slice(last));
  return nodes;
}

function Inline({ text }: { text: string }) {
  return <>{inlineNodes(text)}</>;
}

const EN: Paper = {
  lang: "en",
  kicker: "Whitepaper · Engram AI",
  title: "The engine is the truth, the model is the voice",
  subtitle: "A thesis against the current, on the data that matters.",
  back: "Back to home",
  otherHref: "/fr/engram-ai",
  blocks: [
    { k: "h2", t: "Against the current" },
    { k: "p", t: `A researcher searches. I chose to search against the current.` },
    { k: "p", t: `The artificial intelligence of this decade moves in one direction only: bigger, and further from the ground. Models that double in size from one year to the next, trained on continents of data, housed in compute halls no one ever visits, and trusted more and more with the task of saying what is true. I took the other road, and this text explains why.` },
    { k: "p", t: `Searching against the grain, here, means aiming at four things the race to scale has pushed into the background. **Frugality** first: a system that runs on an ordinary machine and computes little, in place of a bottomless appetite for more. **Sovereignty** next: the data and the engine stay with whoever owns them, they do not travel elsewhere to be judged. **Privacy**, which follows from that and stops being a promise to become a property of the design. And above all, the most misunderstood of the four: giving the language model back the one job it truly does well, language and voice, by taking away the one we wrongly hand it, saying what is true.` },
    { k: "p", t: `The thread that ties these four aims together runs back to the neuroscience of memory, not to computer science. The idea holds in an image I stand by: take what a living brain does with a memory trace and transpose it into an algorithm of **cold** determinism, then add the language model only on top, as the **warm** voice of the tool. The cold holds the truth; the warm puts it into words. Everything that follows comes from that separation.` },

    { k: "h2", t: "The observation" },
    { k: "p", t: `We gave machines speech before we gave them truth.` },
    { k: "p", t: `A large language model phrases things admirably and knows nothing. Give it a precise fact, a number, a date, a clause, a decision made eight months ago, and it returns the most **plausible** run of words, with no way of knowing whether that run is true. On public data, this passes most of the time. On yours, a client file, a regulated number, a contractual commitment, a medical result, the same plausibility turns against you: it takes on the look of an answer, and it lies.` },
    { k: "p", t: `The industry answers this flaw with RAG: retrieve nearby passages and hand them to the model. Retrieving neighboring text is not the same as remembering, and nearness has never made anything true. A setup like this ignores what actually makes a memory: contradiction, the decision that cancels an earlier one, the corroborated fact you tell apart from the fact seen only once, the matter that closes, the value a computation can check. It relocates the problem without solving it. The model no longer hallucinates about its memory; now it hallucinates about the context it was handed. The judge is still the model.` },
    { k: "p", t: `This is the original sin of AI systems laid over enterprise data: **the model sits at the center**. It is the one that decides what is true, at the instant you read it. As long as that seat does not move, no prompt, no fine-tuning, no guardrail makes the system reliable about a fact. You do not fix an architecture with an instruction.` },

    { k: "h2", t: "The thesis" },
    { k: "lead", t: `On private or enterprise data, the language model must never be the source of truth, nor the judge at the moment of reading. You build a deterministic engine that owns the truth, the provenance, and the confidence. The model is only the natural-language interface: the voice. Never the judge.` },
    { k: "p", t: `This shift goes deeper than an optimization: it changes the hand that decides. Truth is computed ahead of time, by code you can audit, test, and replay. When a question comes in, the model receives a fact already established, with its confidence and its source, and it has one thing to do, say it well. It settles nothing at the moment the user asks.` },
    { k: "p", t: `Here the metaphor turns technical. The deterministic engine is the **cold** part of the system: a computed, verified trace that does not change its mind between two readings, much like the engram that neuroscience describes as the physical substrate of a memory. The model is the **warm** part: speech, supple, sensitive to context, giving the fact a human form. The cold guarantees; the warm welcomes. Separating the two is a way of refusing to let speech pass itself off as memory.` },
    { k: "p", t: `From this reversal comes a discipline. It holds in seven principles, and these principles are **technology-agnostic**: they hold for an ERP, a CRM, an internal line-of-business tool as much as for a personal assistant.` },

    { k: "h2", t: "The seven principles" },
    {
      k: "principles",
      items: [
        { n: 1, t: "Truth is computed, not generated.", b: `Facts are established at the point of entry, in plain and structured form, then read back as they are, with nothing reinvented on the fly. When a model helps extract them, it may not keep a single one it cannot point to in the source document; whatever it cannot show, it drops. A guessed fact is never served.` },
        { n: 2, t: "An identifier is proven before it is believed.", b: `A national ID, an IBAN, a case reference counts for what it claims to be only once it has passed the computation that validates it. An act number wrongly taken for a national ID fails that check, and so it is never displayed as one. Where the model would guess, the computation decides.` },
        { n: 3, t: "Every fact carries its source, its state, and its confidence.", b: `Nothing is shown bare. Every fact shows where it comes from, where it stands in its life cycle, proposed, in force, superseded, closed, and how far it can be trusted. The machine states its confidence, and the model is not allowed to inflate it. The brain already keeps this ledger: it holds the content of a memory side by side with its provenance, and it is when the thread of that provenance is lost that it starts to fabricate false memories (Johnson, Hashtroudi and Lindsay, 1993). Knowing whom a fact belongs to is a matter of honesty.` },
        { n: 4, t: "Contradictions are settled by computation, away from reading time.", b: `When two facts contradict each other, the resolution is computed, not negotiated in the moment. If a subtle case calls for a model's opinion, the model gives it off to the side, at write time, and its verdict is filed away as one more piece of data. At reading time, no model judges anymore: what gets served is a verdict already reached.` },
        { n: 5, t: "Memory has structure.", b: `Remembering is far more than laying hands again on neighboring text. It is holding together what belongs together, keeping vivid what matters and letting the rest fade without erasing it entirely, treating a contradiction or a decision as a first-class object. Living brains showed the way: what fires together ends up wired together (Hebb, 1949), and what we do not revisit fades along a curve we have known for more than a century (Ebbinghaus, 1885). The aim is a memory, where an index settles for retrieval.` },
        { n: 6, t: "The trust boundary lives in the code.", b: `Content of doubtful origin, an email, a web page, anything at all that anyone can fabricate, is marked as such and can trigger no action. The slightest effect on the world, writing, sending, modifying, goes through a human confirmation and leaves a trace. The rule allows no exception: you never place within the model's reach a piece of data or a power it is not allowed to emit. The ban lives in the code, not in the prompt.` },
        { n: 7, t: "Confidence governs behavior.", b: `"I don't know" is a full and legitimate answer. The psychology of eyewitness testimony showed this half a century ago: you can be sure of yourself, precise down to the smallest detail, and wrong from start to finish (Loftus and Palmer, 1974). A confident answer tells you nothing about whether it is right. When certainty is missing, the value is not even handed to the model: abstention is not hoped for from an instruction, it is made unavoidable by what the code chooses to withhold. A trustworthy system knows how to stay silent, and it would rather decline than mislead.` },
      ],
    },

    { k: "h2", t: "What an organization gains" },
    { k: "p", t: `This design was built for the places where a mistake has a cost, not for a demo. On compliance, it offers plainly what the GDPR, the AI Act, and regulated professions ask for: a source on every assertion, auditable actions, an abstention that leaves a trace. A system you can certify, where the model-at-the-center stays, at best, probable.` },
    { k: "p", t: `Then comes ordinary trust, the kind of everyday use. The machine asserts what it has computed and shows it; when it doubts, it says so; when it does not know, it stays quiet; and it never hides where it got what it puts forward. That is what makes it usable where plausible-but-wrong is expensive.` },
    { k: "p", t: `The cold has a price too, in the good sense of the word. At reading time, a factual answer is a computation, not a call to the model: faster, cheaper, and above all reproducible, the same question giving the same answer. Inference goes back to what it should have stayed, a commodity you draw on only at the edges. This is frugality held by construction, not promised in a slogan.` },
    { k: "p", t: `Sovereignty remains, which was the starting point. The engine of truth lives with you. The model, for its part, is interchangeable and may never leave the machine. What gains value over time, the fabric of facts, decisions, and contradictions, belongs to you and thickens as you use it.` },

    { k: "h2", t: "The existence proof" },
    { k: "p", t: `This thesis did not stay on paper. It is **implemented, deployed, and audited** in Hygur, a local-first digital twin that takes in a person's informational life, their email, their calendar, their documents, and answers about their facts without ever inventing.` },
    { k: "p", t: `The demonstration takes a minute. Ask Hygur for a precise number: it returns **the value**, sourced, together with its confidence, or it honestly declines when the attribution stays ambiguous. Then cut off the language model. Hygur goes on searching, retrieving, answering about its facts. The truth had not moved, because it did not depend on the model. The engine held the truth; the model was only the voice we had just silenced.` },
    { k: "p", t: `The core of Hygur already bears the name: the **Engram**. In neuroscience, the engram is the *physical trace* of a memory, the substrate where a memory is inscribed and later found again. The word is Richard Semon's, who coined it in 1904 to name the lasting mark a stimulus leaves in a living organism. A century later, Susumu Tonegawa's team made it observable, then manipulable: by switching the neurons of a memory back on in a mouse, they brought the memory itself back (Liu, Ramirez et al., *Nature*, 2012). The engram stopped being a vague picture and became a trace you go looking for and find again. That is precisely what we set against the model's hallucination: a verified, sourced, computed trace. **Recall, not hallucinate**: the recall of a fact on record rather than the invention of a plausible one.` },
    { k: "p", t: `Hygur is the reference implementation, the proof that the thesis stands up away from the whiteboard. The method travels. It is the method this text defends, and the method an organization can carry into its own tool, its ERP, its CRM, its ITSM, its business database, without having to recode Hygur.` },

    { k: "h2", t: "The call" },
    { k: "p", t: `If you have deployed a language model on your data, you already know the wall: it is brilliant, and it lies about your facts. The gain will come from a better **division of roles**, not from a bigger model. Give the truth back to a deterministic engine. Keep the model for what it does well, speaking.` },
    { k: "p", t: `Build the engine. Give it the truth, the provenance, the confidence. Let the model be the voice, and never the judge. It asks more than a coat of API paint, and that is exactly what makes it hold: a system you can audit and certify, and one that lasts.` },
    { k: "doctrine", t: `The engine is the truth. The model is the voice. Recall, not hallucinate.` },

    {
      k: "sources",
      h: "Sources: the neuroscience behind Engram AI",
      intro: `The Engram AI category takes its name from a century of memory research, research that described, measured, and at times filmed the very thing we ask of a machine: to hold on to a verified trace instead of producing a plausible string. These are the works that inform each of Hygur's mechanisms.`,
      items: [
        `**Richard Semon (1904), *Die Mneme*.** Coins the term "engram": the lasting memory trace that a stimulus leaves in a living organism. He gives Engram AI its name and its metaphor.`,
        `**Susumu Tonegawa (Liu, Ramirez et al.), 2012, *Nature*, "Optogenetic stimulation of a hippocampal engram activates fear memory recall."** Makes the engram observable and manipulable through optogenetics: reactivating the neurons of a memory is enough to bring it back. The engram stops being a blurred image; it becomes a trace you find again, the very thing Hygur sets against hallucination.`,
        `**Donald Hebb (1949), *The Organization of Behavior*.** Formulates the plasticity that bears his name: neurons that fire together end up wiring together, with an emphasis on temporal causality. The foundation for the association between memories (principle 5).`,
        `**Hermann Ebbinghaus (1885), *Über das Gedächtnis* (trans. *Memory: A Contribution to Experimental Psychology*).** Describes the forgetting curve and the spacing effect: distributed practice retains better than cramming. Inspires retention, what stays vivid and what fades (principle 5).`,
        `**Diekelmann & Born (2010), *Nature Reviews Neuroscience*, 11:114-126, "The memory function of sleep."** Show that sleep consolidates memory: hippocampal replay during slow-wave sleep, then redistribution toward the neocortex. Inspires nightly consolidation, what Hygur calls "when Hygur dreams."`,
        `**Johnson, Hashtroudi & Lindsay (1993), *Psychological Bulletin*, 114:3-28, "Source monitoring."** Establish that the brain keeps both the content of a memory and its source, and that source error (cryptomnesia among them) is a major cause of false memories. Inspires honesty of attribution: knowing whom a fact belongs to (principle 3).`,
        `**Loftus & Palmer (1974), *Journal of Verbal Learning and Verbal Behavior*, "Reconstruction of Automobile Destruction."** Describe the misinformation effect: information received after the event distorts the memory, and one can be confident, precise, and wrong. Inspires the rule "decline rather than mislead" (principle 7).`,
      ],
    },
  ],
};

const FR: Paper = {
  lang: "fr",
  kicker: "Livre blanc · Engram AI",
  title: "Le moteur est la vérité, le modèle est la voix",
  subtitle: "Une thèse à contre-courant, sur la donnée qui compte.",
  back: "Retour à l'accueil",
  otherHref: "/engram-ai",
  blocks: [
    { k: "h2", t: "À contre-courant" },
    { k: "p", t: `Un chercheur cherche. J'ai choisi de chercher à contre-courant.` },
    { k: "p", t: `L'intelligence artificielle de cette décennie n'avance que dans un sens : plus gros, plus loin du sol. Des modèles qui doublent de taille d'une année sur l'autre, entraînés sur des continents de données, hébergés dans des salles de calcul que personne ne visite, et à qui l'on confie de plus en plus le soin de dire le vrai. J'ai pris l'autre route, et ce texte explique pourquoi.` },
    { k: "p", t: `Chercher à rebours, ici, c'est viser quatre choses que la course à l'échelle a reléguées au second plan. La **frugalité** d'abord : un système qui tient sur une machine ordinaire et calcule peu, au lieu d'un appétit sans fond. La **souveraineté** ensuite : la donnée et le moteur restent chez celui à qui ils appartiennent, ils ne partent pas se faire juger ailleurs. La **vie privée**, qui découle de la précédente et cesse d'être une promesse pour devenir une propriété du montage. Et surtout, la plus mal comprise : rendre au modèle de langage le seul travail qu'il fait vraiment bien, la langue et la voix, en lui retirant celui qu'on lui confie à tort, dire ce qui est vrai.` },
    { k: "p", t: `Le fil qui relie ces quatre visées ne vient pas de l'informatique. Il vient des neurosciences de la mémoire. L'idée tient en une image que j'assume : transposer ce que le vivant fait d'une trace mnésique dans un algorithme au déterminisme **froid**, et n'ajouter le modèle de langage que par-dessus, comme la voix **chaude** de l'outil. Le froid tient la vérité ; le chaud la met en mots. Tout ce qui suit découle de cette séparation.` },

    { k: "h2", t: "Le constat" },
    { k: "p", t: `Nous avons donné à des machines la parole avant de leur donner la vérité.` },
    { k: "p", t: `Un grand modèle de langage formule admirablement et ne sait rien. Posez-lui un fait précis, un numéro, une date, une clause, une décision prise il y a huit mois, et il rend la suite de mots la plus **plausible**, sans aucun moyen de savoir si elle est vraie. Sur des données publiques, cela passe le plus souvent. Sur les vôtres, un dossier client, un numéro réglementé, un engagement contractuel, un résultat médical, la même plausibilité se retourne : elle prend l'apparence d'une réponse et vous ment.` },
    { k: "p", t: `L'industrie répond à ce défaut par le RAG : retrouver des passages proches et les tendre au modèle. Retrouver un texte voisin, ce n'est pourtant pas se souvenir, et le voisinage n'a jamais fait la vérité. Un tel montage ignore ce qui fait une mémoire : la contradiction, la décision qui en annule une autre, le fait corroboré qu'on distingue du fait vu une seule fois, le dossier qui se referme, la valeur qu'un calcul peut vérifier. Il déplace le problème sans le résoudre. Le modèle n'hallucine plus sur sa mémoire, il hallucine sur le contexte qu'on lui a servi. Le juge reste le modèle.` },
    { k: "p", t: `C'est le péché originel des systèmes d'IA posés sur la donnée d'entreprise : **le modèle est au centre**. C'est lui qui décide de ce qui est vrai, à l'instant où on le lit. Tant que cette place ne bouge pas, aucun prompt, aucun fine-tuning, aucun garde-fou ne rend le système fiable sur un fait. On ne corrige pas une architecture avec une consigne.` },

    { k: "h2", t: "La thèse" },
    { k: "lead", t: `Sur de la donnée privée ou d'entreprise, le modèle de langage ne doit jamais être la source de vérité, ni le juge au moment de la lecture. On construit un moteur déterministe qui possède la vérité, la provenance et la confiance. Le modèle n'est que l'interface en langage naturel : la voix. Jamais le juge.` },
    { k: "p", t: `Le déplacement est plus profond qu'une optimisation : il change la main qui décide. La vérité se calcule en amont, par du code qu'on peut auditer, tester, rejouer. Quand la question arrive, le modèle reçoit un fait déjà établi, avec sa confiance et sa source, et il n'a qu'une chose à faire, le dire bien. Il ne tranche rien au moment où l'utilisateur l'interroge.` },
    { k: "p", t: `C'est ici que la métaphore devient technique. Le moteur déterministe est la part **froide** du système : une trace calculée, vérifiée, qui ne change pas d'avis entre deux lectures, à l'image de l'engramme que les neurosciences décrivent comme le substrat physique d'un souvenir. Le modèle est la part **chaude** : la parole, souple, sensible au contexte, qui donne au fait une forme humaine. Le froid garantit ; le chaud accueille. Séparer les deux, c'est refuser que la parole se fasse passer pour de la mémoire.` },
    { k: "p", t: `De ce renversement découle une discipline. Elle tient en sept principes, et ces principes sont **agnostiques de la technologie** : ils valent pour un ERP, un CRM, un outil métier interne autant que pour un assistant personnel.` },

    { k: "h2", t: "Les sept principes" },
    {
      k: "principles",
      items: [
        { n: 1, t: "La vérité se calcule, elle ne se génère pas.", b: `Les faits sont établis dès l'entrée, en clair et en structuré, puis relus tels quels sans que rien ne soit réinventé à la volée. Quand un modèle aide à les extraire, il n'a pas le droit d'en garder un seul qu'il ne puisse pointer dans le document d'origine ; ce qu'il ne peut pas montrer, il le laisse tomber. On ne sert jamais un fait deviné.` },
        { n: 2, t: "Un identifiant se prouve avant d'être cru.", b: `Un numéro national, un IBAN, une référence de dossier ne comptent pour ce qu'ils prétendent être qu'une fois passé le calcul qui les valide. Un numéro d'acte pris à tort pour un numéro national échoue à ce contrôle, et ne sera donc jamais affiché comme tel. Là où le modèle devinerait, le calcul décide.` },
        { n: 3, t: "Chaque fait porte sa source, son état et sa confiance.", b: `Rien ne s'affiche nu. Chaque fait montre d'où il vient, où il en est de son cycle de vie, proposé, en vigueur, remplacé, clos, et à quel point on peut s'y fier. La machine annonce sa confiance, et le modèle n'a pas le droit de la gonfler. Le cerveau tient déjà ce registre : il garde côte à côte le contenu d'un souvenir et sa provenance, et c'est en perdant le fil de cette provenance qu'il se met à fabriquer de faux souvenirs (Johnson, Hashtroudi et Lindsay, 1993). Savoir à qui appartient un fait relève de l'honnêteté.` },
        { n: 4, t: "Les contradictions se règlent par le calcul, à l'écart de la lecture.", b: `Quand deux faits se contredisent, la résolution est calculée, pas négociée sur le moment. Si un cas subtil demande l'avis d'un modèle, il le donne à l'écart, à l'écriture, et son verdict est rangé comme une donnée parmi d'autres. À la lecture, plus aucun modèle ne juge : on sert un verdict déjà rendu.` },
        { n: 5, t: "La mémoire a une structure.", b: `Se souvenir, c'est bien plus que remettre la main sur du texte voisin. C'est tenir ensemble ce qui va ensemble, laisser vif ce qui compte et laisser pâlir le reste sans l'effacer tout à fait, traiter une contradiction ou une décision comme des objets de première classe. Le vivant a montré la voie : ce qui s'active ensemble finit par se lier (Hebb, 1949), et ce qu'on ne revoit pas s'estompe selon une courbe qu'on connaît depuis plus d'un siècle (Ebbinghaus, 1885). On vise une mémoire, là où l'index se contente de retrouver.` },
        { n: 6, t: "La frontière de confiance vit dans le code.", b: `Un contenu d'origine douteuse, un e-mail, une page web, tout ce que n'importe qui peut fabriquer, est marqué comme tel et ne peut déclencher aucune action. Le moindre effet sur le monde, écrire, envoyer, modifier, passe par une confirmation humaine et laisse une trace. La règle ne souffre pas d'exception : on ne place jamais dans le champ du modèle une donnée ou un pouvoir qu'il n'a pas le droit d'émettre. L'interdiction est dans le code, pas dans le prompt.` },
        { n: 7, t: "La confiance commande le comportement.", b: `« Je ne sais pas » est une réponse de plein droit. La psychologie du témoignage l'a montré il y a un demi-siècle : on peut être sûr de soi, précis dans le moindre détail, et se tromper d'un bout à l'autre (Loftus et Palmer, 1974). Qu'une réponse soit assurée ne dit rien de sa justesse. Quand la certitude manque, la valeur n'est même pas remise au modèle : l'abstention n'est pas espérée d'une consigne, elle est rendue inévitable par ce que le code choisit de taire. Un système digne de confiance sait se taire, et il aime mieux décliner que d'induire en erreur.` },
      ],
    },

    { k: "h2", t: "Ce que gagne une organisation" },
    { k: "p", t: `Ce montage n'a pas été pensé pour une démo, mais pour les endroits où une erreur se paie. Côté conformité, il offre sans détour ce que le RGPD, l'AI Act et les métiers réglementés réclament : une source sur chaque assertion, des actions auditables, une abstention qui laisse une trace. Un système qu'on peut certifier, là où le modèle-au-centre reste, au mieux, probable.` },
    { k: "p", t: `Vient ensuite la confiance ordinaire, celle de l'usage quotidien. La machine affirme ce qu'elle a calculé et le montre ; quand elle doute, elle le dit ; quand elle ignore, elle se tait ; et jamais elle ne cache d'où elle tient ce qu'elle avance. C'est ce qui la rend utilisable là où le plausible-mais-faux coûte cher.` },
    { k: "p", t: `Le froid a aussi un prix, au bon sens du terme. À la lecture, une réponse factuelle est un calcul, pas un appel au modèle : plus rapide, moins chère, et surtout reproductible, la même question rendant la même réponse. L'inférence redevient ce qu'elle aurait dû rester, une commodité qu'on ne sollicite qu'aux frontières. C'est la frugalité tenue par la construction, pas promise dans un slogan.` },
    { k: "p", t: `Reste la souveraineté, qui était le point de départ. Le moteur de vérité vit chez vous. Le modèle, lui, est interchangeable et peut ne jamais quitter la machine. Ce qui prend de la valeur avec le temps, le tissu de faits, de décisions et de contradictions, vous appartient et s'épaissit à mesure que vous l'utilisez.` },

    { k: "h2", t: "La preuve d'existence" },
    { k: "p", t: `Cette thèse n'est pas restée sur le papier. Elle est **implémentée, déployée et auditée** dans Hygur, un double numérique local-first qui absorbe la vie informationnelle d'une personne, ses mails, son agenda, ses documents, et répond sur ses faits sans jamais inventer.` },
    { k: "p", t: `La démonstration tient en une minute. Demandez à Hygur un numéro précis : il rend **la valeur**, sourcée, assortie de sa confiance, ou il refuse honnêtement quand l'attribution reste ambiguë. Coupez ensuite le modèle de langage. Hygur continue de chercher, de retrouver, de répondre sur ses faits. La vérité, elle, n'avait pas bougé, parce qu'elle ne dépendait pas du modèle. Le moteur tenait la vérité ; le modèle n'était que la voix qu'on vient de faire taire.` },
    { k: "p", t: `Le cœur d'Hygur porte d'ailleurs déjà ce nom : l'**Engram**. En neurosciences, l'engramme est la *trace physique* d'un souvenir, le substrat où une mémoire s'inscrit et se retrouve. Le mot est de Richard Semon, qui le forge en 1904 pour nommer la marque durable qu'un stimulus laisse dans le vivant. Un siècle plus tard, l'équipe de Susumu Tonegawa l'a rendu observable, puis manipulable : en rallumant chez la souris les neurones d'un souvenir, on fait revenir le souvenir lui-même (Liu, Ramirez et al., *Nature*, 2012). L'engramme a cessé d'être une image vague pour devenir une trace qu'on va rechercher et qu'on retrouve. C'est très exactement ce que nous opposons à l'hallucination du modèle : une trace vérifiée, sourcée, calculée. **Recall, not hallucinate** : rappeler un fait inscrit plutôt qu'inventer un fait plausible.` },
    { k: "p", t: `Hygur est l'implémentation de référence, la preuve que la thèse tient debout hors du tableau blanc. La **méthode**, elle, voyage. C'est elle que ce texte défend, et c'est elle qu'une organisation peut porter dans son propre outil, son ERP, son CRM, son ITSM, sa base métier, sans avoir à recoder Hygur.` },

    { k: "h2", t: "L'appel" },
    { k: "p", t: `Si vous avez déployé un modèle de langage sur vos données, vous connaissez déjà le mur : il est brillant, et il ment sur vos faits. Le gain viendra d'un meilleur **partage des rôles**, pas d'un modèle plus gros. Rendez la vérité à un moteur déterministe. Gardez le modèle pour ce qu'il réussit, parler.` },
    { k: "p", t: `Construisez le moteur. Confiez-lui la vérité, la provenance, la confiance. Laissez le modèle être la voix, et jamais le juge. C'est plus exigeant qu'un habillage d'API, et c'est justement là que ça devient tenable : un système qu'on peut auditer et certifier, et qui dure.` },
    { k: "doctrine", t: `Le moteur est la vérité. Le modèle est la voix. Recall, not hallucinate.` },

    {
      k: "sources",
      h: "Sources : les neurosciences qui inspirent Engram AI",
      intro: `La catégorie Engram AI tire son nom d'un siècle de recherche sur la mémoire, une recherche qui a décrit, mesuré, parfois filmé ce que nous demandons à une machine : retenir une trace vérifiée au lieu de produire une suite plausible. Ce sont ces travaux qui éclairent chacun des mécanismes d'Hygur.`,
      items: [
        `**Richard Semon (1904), *Die Mneme*.** Forge le terme « engramme » : la trace mnésique durable qu'un stimulus laisse dans le vivant. Il donne son nom et sa métaphore à Engram AI.`,
        `**Susumu Tonegawa (Liu, Ramirez et al.), 2012, *Nature*, « Optogenetic stimulation of a hippocampal engram activates fear memory recall ».** Rend l'engramme observable et manipulable par optogénétique : réactiver les neurones d'un souvenir suffit à le faire revenir. L'engramme cesse d'être une image floue ; c'est une trace qu'on retrouve, ce qu'Hygur oppose à l'hallucination.`,
        `**Donald Hebb (1949), *The Organization of Behavior*.** Formule la plasticité qui porte son nom : des neurones qui s'activent ensemble finissent par se lier, avec une insistance sur la causalité temporelle. Fondement de l'association entre souvenirs (principe 5).`,
        `**Hermann Ebbinghaus (1885), *Über das Gedächtnis* (trad. *Memory: A Contribution to Experimental Psychology*).** Décrit la courbe de l'oubli et l'effet d'espacement : la pratique distribuée retient mieux que le bachotage. Inspire la rétention, ce qui reste vif et ce qui s'estompe (principe 5).`,
        `**Diekelmann & Born (2010), *Nature Reviews Neuroscience*, 11:114-126, « The memory function of sleep ».** Montrent que le sommeil consolide la mémoire : rejeu de l'hippocampe pendant les ondes lentes, puis redistribution vers le néocortex. Inspire la consolidation nocturne, ce qu'Hygur appelle « quand Hygur rêve ».`,
        `**Johnson, Hashtroudi & Lindsay (1993), *Psychological Bulletin*, 114:3-28, « Source monitoring ».** Établissent que le cerveau garde le contenu d'un souvenir et sa source, et que l'erreur de source (dont la cryptomnésie) est une cause majeure de faux souvenirs. Inspire l'honnêteté d'attribution : savoir à qui appartient un fait (principe 3).`,
        `**Loftus & Palmer (1974), *Journal of Verbal Learning and Verbal Behavior*, « Reconstruction of Automobile Destruction ».** Décrivent l'effet de désinformation : une information reçue après l'événement déforme le souvenir, et l'on peut être confiant, précis et faux. Inspire la règle « décliner plutôt qu'induire en erreur » (principe 7).`,
      ],
    },
  ],
};

const PAPERS: Record<"en" | "fr", Paper> = { en: EN, fr: FR };

/** Per-language SEO metadata, consumed by the pre-render (prerender.mjs) to
 *  stamp <title>, description, OpenGraph, canonical and hreflang into the
 *  static HTML head. EN is the default / x-default. */
export const ENGRAM_PAGES = [
  {
    route: "engram",
    dir: "engram-ai",
    lang: "en",
    title: "Engram AI — The engine is the truth, the model is the voice",
    description:
      "On private or enterprise data, the language model must never be the source of truth. Build a deterministic engine that owns the truth, provenance and confidence; let the model be only the voice. A thesis on Engram AI: recall, not hallucinate.",
    url: "https://hygur.ai/engram-ai",
  },
  {
    route: "engram-fr",
    dir: "fr/engram-ai",
    lang: "fr",
    title: "Engram AI — Le moteur est la vérité, le modèle est la voix",
    description:
      "Sur la donnée privée ou d'entreprise, le modèle de langage ne doit jamais être la source de vérité. Construisez un moteur déterministe qui possède la vérité, la provenance et la confiance ; le modèle n'est que la voix. Une thèse sur Engram AI : recall, not hallucinate.",
    url: "https://hygur.ai/fr/engram-ai",
  },
] as const;

export const ENGRAM_HREFLANG = {
  en: "https://hygur.ai/engram-ai",
  fr: "https://hygur.ai/fr/engram-ai",
  x_default: "https://hygur.ai/engram-ai",
};

function LangToggle({ lang, otherHref }: { lang: "en" | "fr"; otherHref: string }) {
  const active =
    "rounded-full bg-accent px-3 py-1 text-xs font-semibold text-bg";
  const inactive =
    "rounded-full px-3 py-1 text-xs font-semibold text-muted transition-colors hover:text-text";
  return (
    <div
      className="flex items-center gap-1 rounded-full border border-border bg-surface p-0.5 shadow-[var(--shadow-soft)]"
      aria-label={lang === "fr" ? "Langue" : "Language"}
    >
      {lang === "en" ? (
        <>
          <span className={active} aria-current="page">
            EN
          </span>
          <a className={inactive} href={otherHref} hrefLang="fr" lang="fr">
            FR
          </a>
        </>
      ) : (
        <>
          <a className={inactive} href={otherHref} hrefLang="en" lang="en">
            EN
          </a>
          <span className={active} aria-current="page">
            FR
          </span>
        </>
      )}
    </div>
  );
}

function renderBlock(b: Block, i: number): ReactNode {
  switch (b.k) {
    case "h2":
      return <h2 key={i}>{b.t}</h2>;
    case "p":
      return (
        <p key={i}>
          <Inline text={b.t} />
        </p>
      );
    case "lead":
      return (
        <div
          key={i}
          className="my-8 rounded-2xl border border-accent/25 bg-accent-weak/50 px-6 py-5"
        >
          <p className="font-display text-lg leading-relaxed text-text">{b.t}</p>
        </div>
      );
    case "principles":
      return (
        <ol key={i} className="my-9 list-none space-y-7 pl-0">
          {b.items.map((it) => (
            <li key={it.n} className="flex gap-4">
              <span className="grid h-8 w-8 shrink-0 place-items-center rounded-full bg-accent-weak text-sm font-semibold text-accent">
                {it.n}
              </span>
              <div>
                <h3 className="font-display text-lg font-semibold leading-snug text-text">
                  {it.t}
                </h3>
                <p className="mt-1.5 leading-relaxed text-muted">
                  <Inline text={it.b} />
                </p>
              </div>
            </li>
          ))}
        </ol>
      );
    case "doctrine":
      return (
        <p
          key={i}
          className="my-12 border-y border-hairline py-8 text-center font-display text-[clamp(1.3rem,3.2vw,1.7rem)] font-semibold leading-snug text-balance text-accent"
        >
          {b.t}
        </p>
      );
    case "sources":
      return (
        <section key={i} className="mt-4">
          <h2>{b.h}</h2>
          <p>
            <Inline text={b.intro} />
          </p>
          <ol className="mt-6 list-none space-y-4 pl-0">
            {b.items.map((s, j) => (
              <li
                key={j}
                className="border-l-2 border-hairline pl-4 text-[0.95rem] leading-relaxed text-muted"
              >
                <Inline text={s} />
              </li>
            ))}
          </ol>
        </section>
      );
  }
}

export function Engram({ lang }: { lang: "en" | "fr" }) {
  const paper = PAPERS[lang];
  return (
    <>
      <header className="sticky top-0 z-50 border-b border-hairline bg-bg/85 backdrop-blur-md">
        <div className="mx-auto flex h-16 max-w-3xl items-center justify-between gap-3 px-5 sm:px-8">
          <a href="/" className="flex items-center gap-2.5" aria-label="Hygur — home">
            <img
              src={logo}
              alt=""
              width={30}
              height={30}
              className="h-[30px] w-[30px] rounded-[9px] shadow-[var(--shadow-soft)]"
            />
            <span className="font-display text-[1.35rem] leading-none text-text">Hygur</span>
          </a>
          <div className="flex items-center gap-3">
            <LangToggle lang={lang} otherHref={paper.otherHref} />
            <Button
              href={RELEASES_URL}
              target="_blank"
              rel="noreferrer"
              variant="ghost"
              className="hidden sm:inline-flex"
            >
              Get the app
              <ArrowUpRight size={16} strokeWidth={2} />
            </Button>
          </div>
        </div>
      </header>

      <main className="mx-auto max-w-2xl px-5 py-14 sm:px-8 lg:py-20">
        <a
          href="/"
          className="inline-flex items-center gap-1.5 text-sm text-muted transition-colors hover:text-text"
        >
          <ArrowLeft size={15} strokeWidth={2} />
          {paper.back}
        </a>

        <div className="mt-8">
          <Eyebrow>{paper.kicker}</Eyebrow>
        </div>
        <h1 className="font-display mt-5 text-[clamp(2.1rem,5vw,3.1rem)] font-semibold leading-[1.04] text-balance text-text">
          {paper.title}
        </h1>
        <p className="mt-4 text-pretty text-lg leading-relaxed text-muted">{paper.subtitle}</p>

        <article className="paper-prose mt-12">
          {paper.blocks.map((b, i) => renderBlock(b, i))}
        </article>
      </main>

      <Footer />
    </>
  );
}
