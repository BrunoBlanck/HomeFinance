import { type QueryClient, useMutation, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import type { z } from 'zod'
import { ApiError } from '@/api/client'
import type { Account, AccountKind, AccountList, Institution } from '@/api/types'
import { Alert } from '@/components/Alert/Alert'
import { Button } from '@/components/Button/Button'
import { Dialog } from '@/components/Dialog/Dialog'
import { KeywordsField } from '@/components/KeywordsField/KeywordsField'
import { MoneyInput } from '@/components/MoneyInput/MoneyInput'
import { Select } from '@/components/Select/Select'
import { TextField } from '@/components/TextField/TextField'
import { useToast } from '@/components/Toast/Toast'
import { accountFormSchema } from '@/lib/account-schema'
import {
  createAccount,
  INSTITUICOES,
  ROTULO_DA_INSTITUICAO,
  ROTULO_DO_TIPO,
  TIPOS_DE_CONTA,
  updateAccount,
} from '@/lib/accounts'
import { keywordTakenOf, messageForError } from '@/lib/errors'
import { indiceDaPalavra, normalizarPalavra } from '@/lib/keywords'
import styles from './AccountDialog.module.css'

/** Dica fixa do campo de palavras-chave — tabela (g) de `docs/DESIGN.md`. */
const DICA_PALAVRAS_CHAVE =
  'Como esta conta aparece nos extratos das OUTRAS contas — «nubank», «pix c6». É o que detecta transferências.'

/** Valores iniciais ao CRIAR uma conta.
 *
 *  Existe para a recuperação da importação: quando a fatura vai para a conta
 *  errada, a tela de importação abre este diálogo já apontando para o tipo
 *  `credit_card` e a instituição detectada no arquivo, com um nome sugerido. Na
 *  edição é ignorado — ali a conta já preenche tudo. */
export type AccountPrefill = {
  kind?: AccountKind | undefined
  institution?: Institution | undefined
  name?: string | undefined
}

type AccountDialogProps = {
  open: boolean
  onClose: () => void
  /** Ausente = criando. Presente = editando aquela conta. */
  account?: Account | undefined
  /** Data de hoje no fuso da casa, em `AAAA-MM-DD`. Vem de fora porque quem
   *  sabe o fuso é a casca, e "hoje" é regra de negócio (ADR-019). */
  hoje: string
  /** Valores iniciais ao criar (ignorados na edição). */
  prefill?: AccountPrefill | undefined
  /** Chamado com a conta recém-CRIADA (nunca na edição), já depois de invalidar
   *  a query de contas. É por aqui que o chamador pré-seleciona a conta nova —
   *  ex.: a importação, para reenviar o arquivo na conta certa. */
  onCreated?: ((conta: Account) => void) | undefined
}

/** Erro do servidor sobre UMA palavra-chave. Guarda a forma normalizada, não o
 *  índice: o índice é derivado da lista atual a cada render, então remover a
 *  ficha marcada apaga o erro sozinho — e reordenar nunca marca a ficha errada. */
type ErroDePalavra = { normalizada: string; mensagem: string }

type Erros = {
  name?: string
  kind?: string
  institution?: string
  statementClosingDay?: string
  statementDueDay?: string
  openingBalanceCents?: string
  openingDate?: string
  keyword?: ErroDePalavra
  geral?: string
}

/** Criar e editar conta.
 *
 *  Formulário controlado à mão, sem react-hook-form: um dos campos tem estado
 *  numérico próprio (`MoneyInput` guarda centavos, não texto) e dois aparecem e
 *  somem conforme o tipo escolhido. A validação é do `accountFormSchema`, que
 *  espelha o que o servidor cobra — mas a autoridade continua sendo o servidor,
 *  e o formulário só evita a viagem óbvia. */
export function AccountDialog({
  open,
  onClose,
  account,
  hoje,
  prefill,
  onCreated,
}: AccountDialogProps) {
  const editando = account !== undefined
  const queryClient = useQueryClient()
  const toast = useToast()

  // Primitivas em vez do objeto `prefill` nas dependências do efeito: um literal
  // novo a cada render reabriria o formulário e apagaria o que a pessoa digitou.
  const prefillName = prefill?.name ?? ''
  const prefillKind = prefill?.kind ?? 'checking'
  const prefillInstitution = prefill?.institution ?? 'other'

  const [name, setName] = useState('')
  const [kind, setKind] = useState<AccountKind>('checking')
  const [institution, setInstitution] = useState<Institution>('other')
  // Dia de fatura é texto no formulário e número no contrato: o campo precisa
  // de um estado "vazio", que em número não existe sem inventar um sentinela.
  const [fechamento, setFechamento] = useState('')
  const [vencimento, setVencimento] = useState('')
  const [openingBalanceCents, setOpeningBalanceCents] = useState(0)
  const [openingDate, setOpeningDate] = useState(hoje)
  const [keywords, setKeywords] = useState<string[]>([])
  const [erros, setErros] = useState<Erros>({})

  const ehCartao = kind === 'credit_card'

  // Reabrir o diálogo precisa recomeçar do estado certo: sem isto, editar uma
  // conta e depois clicar em "Nova conta" traria os dados da anterior.
  useEffect(() => {
    if (!open) return
    setErros({})
    if (account) {
      setName(account.name)
      setKind(account.kind)
      // Instituição fora da allowlist deixaria o `<select>` sem opção
      // correspondente e o campo apareceria em branco; `other` é o default do
      // contrato e o único fallback honesto.
      setInstitution(INSTITUICOES.includes(account.institution) ? account.institution : 'other')
      setFechamento(textoDoDia(account.statementClosingDay))
      setVencimento(textoDoDia(account.statementDueDay))
      setOpeningBalanceCents(account.openingBalanceCents)
      setOpeningDate(account.openingDate)
      setKeywords(account.keywords)
      return
    }
    setName(prefillName)
    setKind(prefillKind)
    setInstitution(prefillInstitution)
    setFechamento('')
    setVencimento('')
    setOpeningBalanceCents(0)
    setOpeningDate(hoje)
    setKeywords([])
  }, [open, account, hoje, prefillName, prefillKind, prefillInstitution])

  // A ficha marcada é a que AINDA está na lista com a forma normalizada do
  // erro. Removida, o índice vira -1 e o erro some — sem efeito nem callback.
  const indiceMarcado = erros.keyword ? indiceDaPalavra(keywords, erros.keyword.normalizada) : -1

  /** Troca de tipo apaga os dias de fatura.
   *
   *  Não é só esconder: enquanto o valor continuasse no estado, ele seguiria no
   *  corpo da requisição e o servidor devolveria 422 por um campo que a pessoa
   *  não vê mais na tela. */
  function trocarTipo(novo: AccountKind) {
    setKind(novo)
    if (novo === 'credit_card') return
    setFechamento('')
    setVencimento('')
    // Descarta as duas chaves em vez de atribuir `undefined`: com
    // `exactOptionalPropertyTypes`, campo ausente e campo `undefined` não são a
    // mesma coisa, e só o primeiro significa "não há erro aqui".
    setErros(
      ({ statementClosingDay: _fechamento, statementDueDay: _vencimento, ...resto }) => resto,
    )
  }

  const salvar = useMutation({
    mutationFn: async (valores: z.infer<typeof accountFormSchema>) => {
      if (account) {
        // PATCH: campo ausente é "não mexi". Os dias vão SEMPRE, e vão nulos
        // quando a conta deixou de ser cartão — é esse nulo explícito que limpa
        // no servidor um fechamento que ficaria órfão (ver `UpdateAccountRequest`).
        return updateAccount(account.id, valores)
      }
      // POST: em conta que não é cartão os dias nem existem no corpo. Ausente é
      // inequívoco; mandar a chave, ainda que nula, é pedir ao servidor que
      // interprete.
      const { statementClosingDay, statementDueDay, ...base } = valores
      return createAccount(
        valores.kind === 'credit_card' ? { ...base, statementClosingDay, statementDueDay } : base,
      )
    },
    onSuccess: async (conta) => {
      await queryClient.invalidateQueries({ queryKey: ['accounts'] })
      toast.sucesso(editando ? 'Conta atualizada.' : 'Conta criada.')
      // Só na criação, e antes de fechar: o chamador recebe a conta nova para
      // pré-selecioná-la (a importação reenvia o arquivo na conta certa).
      if (!editando) onCreated?.(conta)
      onClose()
    },
    onError: (error) => setErros(traduzir(error, keywords, queryClient)),
  })

  function enviar(event: React.FormEvent) {
    event.preventDefault()
    if (salvar.isPending) return

    const resultado = accountFormSchema.safeParse({
      name,
      kind,
      institution,
      statementClosingDay: diaDigitado(fechamento),
      statementDueDay: diaDigitado(vencimento),
      openingBalanceCents,
      openingDate,
      keywords,
    })
    if (!resultado.success) {
      setErros(errosDoSchema(resultado.error, keywords))
      return
    }
    setErros({})
    salvar.mutate(resultado.data)
  }

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title={editando ? 'Editar conta' : 'Nova conta'}
      description={
        editando
          ? 'O saldo de abertura é o que havia na conta no dia em que ela entrou aqui.'
          : 'Informe o saldo que a conta tinha no dia em que você começou a registrar.'
      }
      footer={
        <>
          <Button variant="quiet" onClick={onClose}>
            Cancelar
          </Button>
          <Button variant="primary" type="submit" form="form-conta" loading={salvar.isPending}>
            {editando ? 'Salvar' : 'Criar conta'}
          </Button>
        </>
      }
    >
      <form id="form-conta" onSubmit={enviar} noValidate className={styles.form}>
        {erros.geral ? <Alert tone="error">{erros.geral}</Alert> : null}

        <TextField
          label="Nome"
          value={name}
          onChange={(event) => setName(event.target.value)}
          maxLength={80}
          autoComplete="off"
          autoFocus
          error={erros.name}
          hint="Como você chama esta conta no dia a dia."
        />

        <Select
          label="Tipo"
          value={kind}
          onChange={(event) => trocarTipo(event.target.value as AccountKind)}
          options={TIPOS_DE_CONTA.map((tipo) => ({ value: tipo, label: ROTULO_DO_TIPO[tipo] }))}
          error={erros.kind}
          {...(ehCartao ? { hint: 'No cartão, o saldo devedor é um valor negativo.' } : {})}
        />

        <Select
          label="Instituição"
          value={institution}
          onChange={(event) => setInstitution(event.target.value as Institution)}
          options={INSTITUICOES.map((banco) => ({
            value: banco,
            label: ROTULO_DA_INSTITUICAO[banco],
          }))}
          error={erros.institution}
          hint="Ao importar, confere se o arquivo é mesmo desta conta."
        />

        {ehCartao ? (
          <fieldset className={styles.fatura}>
            <legend className={styles.legenda}>Fatura do cartão</legend>
            <p className={styles.explicacao}>
              Com o fechamento e o vencimento preenchidos, a importação da fatura já sugere a
              competência e as duas datas certas. Sem eles, o app precisa adivinhar pela maior data
              do arquivo — e erra justamente na virada do mês.
            </p>
            <div className={styles.dias}>
              <TextField
                label="Dia do fechamento"
                value={fechamento}
                onChange={(event) => setFechamento(soDigitos(event.target.value))}
                inputMode="numeric"
                autoComplete="off"
                maxLength={2}
                placeholder="03"
                error={erros.statementClosingDay}
                hint="De 1 a 31."
              />
              <TextField
                label="Dia do vencimento"
                value={vencimento}
                onChange={(event) => setVencimento(soDigitos(event.target.value))}
                inputMode="numeric"
                autoComplete="off"
                maxLength={2}
                placeholder="10"
                error={erros.statementDueDay}
                hint="De 1 a 31."
              />
            </div>
          </fieldset>
        ) : null}

        <MoneyInput
          label="Saldo de abertura"
          value={openingBalanceCents}
          onChange={setOpeningBalanceCents}
          allowNegative
          error={erros.openingBalanceCents}
          hint="Use o botão − quando a conta estiver negativa."
        />

        <TextField
          label="Data do saldo"
          type="date"
          value={openingDate}
          onChange={(event) => setOpeningDate(event.target.value)}
          error={erros.openingDate}
          hint="A partir desta data os lançamentos começam a contar."
        />

        {/* Último campo: é o opcional e avançado — por último, não interrompe
            quem só quer dar um nome e um saldo. */}
        <KeywordsField
          value={keywords}
          onChange={setKeywords}
          hint={DICA_PALAVRAS_CHAVE}
          error={indiceMarcado >= 0 ? erros.keyword?.mensagem : undefined}
          invalidIndex={indiceMarcado >= 0 ? indiceMarcado : undefined}
        />
      </form>
    </Dialog>
  )
}

/** Mantém só os dois dígitos que um dia do mês pode ter.
 *
 *  Filtrar na digitação em vez de usar `type="number"`: o input numérico
 *  devolve `''` para qualquer coisa que o navegador considere inválida, e a
 *  pessoa que digita "3o" perde o que escreveu sem saber por quê. */
function soDigitos(texto: string): string {
  return texto.replace(/\D/g, '').slice(0, 2)
}

function diaDigitado(texto: string): number | null {
  return texto === '' ? null : Number(texto)
}

function textoDoDia(dia: number | null): string {
  return typeof dia === 'number' ? String(dia) : ''
}

/** Primeira mensagem de cada campo que o schema reprovou.
 *
 *  As frases já nascem em português no schema; aqui só se escolhe uma por
 *  campo, porque o slot de mensagem do campo mostra uma. */
function errosDoSchema(error: z.ZodError, keywords: readonly string[]): Erros {
  const erros: Erros = {}
  for (const issue of error.issues) {
    switch (issue.path[0]) {
      case 'name':
      case 'kind':
      case 'institution':
      case 'statementClosingDay':
      case 'statementDueDay':
      case 'openingBalanceCents':
      case 'openingDate':
        erros[issue.path[0]] ??= issue.message
        break
      // `['keywords', i]` é uma ficha; `['keywords']` é a lista (o limite).
      case 'keywords': {
        const palavra = typeof issue.path[1] === 'number' ? keywords[issue.path[1]] : undefined
        if (palavra !== undefined) {
          erros.keyword ??= { normalizada: normalizarPalavra(palavra), mensagem: issue.message }
        } else {
          erros.geral ??= issue.message
        }
        break
      }
      // Sem caminho não há campo a marcar; a frase vira o erro do formulário
      // em vez de sumir.
      default:
        erros.geral ??= issue.message
    }
  }
  return erros
}

/** O nome da conta `id` em qualquer lista que esteja no cache — com ou sem
 *  arquivadas. Só o nome: o `ownerId` nunca chega à tela cru. */
function nomeDaContaNoCache(queryClient: QueryClient, id: string): string | undefined {
  for (const [, lista] of queryClient.getQueriesData<AccountList>({ queryKey: ['accounts'] })) {
    const conta = lista?.items.find((item) => item.id === id)
    if (conta) return conta.name
  }
  return undefined
}

/** 409 `KEYWORD_TAKEN` → a ficha e a frase da tabela (g): "«nubank» já está
 *  na conta Nubank." — ou, quando a dona não está no cache (corrida rara em
 *  que o índice único decidiu), "…já está em outra conta desta casa." */
function erroDePalavraTomada(
  error: ApiError,
  keywords: readonly string[],
  queryClient: QueryClient,
): ErroDePalavra | undefined {
  const conflito = keywordTakenOf(error)
  if (!conflito) return undefined
  const normalizada = normalizarPalavra(conflito.keyword)
  // A palavra como a pessoa a digitou, quando ainda está na lista; senão a
  // que o servidor devolveu.
  const palavra = keywords[indiceDaPalavra(keywords, normalizada)] ?? conflito.keyword
  const dona = conflito.ownerId ? nomeDaContaNoCache(queryClient, conflito.ownerId) : undefined
  return {
    normalizada,
    mensagem: dona
      ? `«${palavra}» já está na conta ${dona}.`
      : `«${palavra}» já está em outra conta desta casa.`,
  }
}

/** 400 `fields.keywords[i]` → a ficha `i` com a frase da tabela (g). */
function erroDePalavraInvalida(
  campo: string,
  keywords: readonly string[],
): ErroDePalavra | undefined {
  const indice = /^keywords\[(\d+)\]$/.exec(campo)?.[1]
  if (indice === undefined) return undefined
  const palavra = keywords[Number(indice)]
  if (palavra === undefined) return undefined
  return {
    normalizada: normalizarPalavra(palavra),
    mensagem: `«${palavra}» não é uma palavra-chave válida: de 2 a 40 caracteres, só letras, números, espaço e & . - / '`,
  }
}

/** Traduz a resposta do servidor em erros por campo.
 *
 *  O servidor manda os NOMES dos campos inválidos, nunca texto pronto para a
 *  tela (docs/SEGURANCA.md §4: mensagem ao cliente é genérica). Quem escreve a
 *  frase em português é aqui. */
function traduzir(error: unknown, keywords: readonly string[], queryClient: QueryClient): Erros {
  if (!(error instanceof ApiError)) return { geral: messageForError(error) }

  const tomada = erroDePalavraTomada(error, keywords, queryClient)
  if (tomada) return { keyword: tomada }

  if (error.invalidFields.length > 0) {
    const erros: Erros = {}
    for (const campo of error.invalidFields) {
      const palavraInvalida = erroDePalavraInvalida(campo, keywords)
      if (palavraInvalida) {
        // Uma ficha marcada por vez: a primeira que o servidor apontou.
        erros.keyword ??= palavraInvalida
        continue
      }
      switch (campo) {
        case 'name':
          erros.name = 'Já existe uma conta com este nome, ou o nome é inválido.'
          break
        case 'kind':
          erros.kind = 'Escolha um tipo de conta.'
          break
        case 'institution':
          erros.institution = 'Escolha uma instituição da lista.'
          break
        case 'statementClosingDay':
          erros.statementClosingDay = 'Informe um dia entre 1 e 31, ou deixe em branco.'
          break
        case 'statementDueDay':
          erros.statementDueDay = 'Informe um dia entre 1 e 31, ou deixe em branco.'
          break
        case 'openingBalanceCents':
          erros.openingBalanceCents = 'Valor fora da faixa permitida.'
          break
        case 'openingDate':
          erros.openingDate = 'Informe uma data válida.'
          break
        case 'keywords':
          erros.geral = 'Limite de 20 palavras-chave. Remova uma para incluir outra.'
          break
        case 'limit':
          erros.geral = 'Você atingiu o limite de contas desta casa.'
          break
        default:
          erros.geral = 'Confira os dados informados e tente de novo.'
      }
    }
    return erros
  }

  return { geral: messageForError(error) }
}
