import {
  type ChangeEvent,
  type ClipboardEvent,
  type KeyboardEvent,
  type PointerEvent,
  useEffect,
  useId,
  useRef,
  useState,
} from 'react'
import { indiceDaPalavra, MAX_PALAVRAS_CHAVE, validarPalavra } from '@/lib/keywords'
import { FieldShell } from '../FieldShell/FieldShell'
import { AlertIcon } from '../icons/AlertIcon'
import { CloseIcon } from '../icons/CloseIcon'
import styles from './KeywordsField.module.css'

/** Copy fixa do campo (tabela (g) de `docs/DESIGN.md`). */
const DICA_NO_LIMITE = 'Limite de 20. Remova uma palavra para incluir outra.'
const INSTRUCAO = 'Use as setas para percorrer as palavras e Backspace para remover.'

type KeywordsFieldProps = {
  /** A lista como está no formulário — o campo é controlado. Cada item é a
   *  palavra COMO FOI DIGITADA; a comparação é pela forma normalizada. */
  value: readonly string[]
  onChange: (proxima: string[]) => void
  /** Dica fixa do diálogo (categoria ou conta). Em 20 de 20 é trocada pela de
   *  limite; com erro em jogo, some — hint OU erro, regra da FieldShell. */
  hint: string
  /** Erro do SERVIDOR sobre uma ficha: 400 `fields.keywords[i]` ou 409
   *  `KEYWORD_TAKEN`. A mensagem vai na FieldShell e a ficha `invalidIndex`
   *  recebe borda e ícone. Quem deriva o índice da lista atual é o diálogo —
   *  por isso remover a ficha marcada apaga o erro sem que este componente
   *  precise avisar ninguém. */
  error?: string | undefined
  invalidIndex?: number | undefined
  label?: string | undefined
}

/** O que precisa receber o foco DEPOIS que a lista mudar. Guardado em ref e
 *  aplicado num efeito porque a lista é do pai: só depois que ele re-renderizar
 *  é que o × seguinte existe no DOM. */
type FocoPendente = number | 'input' | null

/** Campo de palavras-chave: fichas dentro de uma caixa que é a mesma "linha do
 *  caderno" do TextField, com o input no fim.
 *
 *  Decisão (a) da seção E2c de `docs/DESIGN.md`. O que este componente
 *  garante, e que os testes prendem:
 *
 *  - `Enter` e `,` adicionam; `Enter` NUNCA submete o formulário daqui, com ou
 *    sem texto. Colar divide por vírgula e por quebra de linha.
 *  - `Backspace` com o input vazio remove a última ficha; `←` no início do
 *    input vai ao × da última; `←`/`→` percorrem as fichas; `→` na última
 *    volta ao input; `Delete`/`Backspace`/`Enter`/`Space` num × remove, e o
 *    foco vai para a seguinte, senão a anterior, senão o input.
 *  - Os × têm `tabIndex={-1}` (padrão APG de tabindex rodante): vinte fichas
 *    não podem ser vinte paradas de Tab dentro de um diálogo.
 *  - Duplicata e palavra inválida não entram; o input mantém o texto e a
 *    mensagem diz por quê — e some na próxima tecla.
 *  - Em 20 de 20 o input fica `readOnly`, nunca `disabled`: continua focável
 *    e o `Backspace` continua removendo.
 *  - O contador `N de 20` é a ÚNICA live region do campo. */
export function KeywordsField({
  value,
  onChange,
  hint,
  error,
  invalidIndex,
  label = 'Palavras-chave',
}: KeywordsFieldProps) {
  const reactId = useId()
  const inputId = `${reactId}-field`
  const mensagemId = `${reactId}-message`
  const contadorId = `${reactId}-count`
  const instrucaoId = `${reactId}-keys`

  const [texto, setTexto] = useState('')
  const [mensagemLocal, setMensagemLocal] = useState<string | undefined>(undefined)

  const inputRef = useRef<HTMLInputElement>(null)
  const botoesRef = useRef<(HTMLButtonElement | null)[]>([])
  const focoPendente = useRef<FocoPendente>(null)

  const cheio = value.length >= MAX_PALAVRAS_CHAVE
  // A mensagem local (duplicata, inválida) fala da tecla que acabou de ser
  // apertada, então vence a do servidor enquanto existir.
  const erro = mensagemLocal ?? error

  useEffect(() => {
    const alvo = focoPendente.current
    if (alvo === null) return
    focoPendente.current = null
    if (alvo === 'input') inputRef.current?.focus()
    else botoesRef.current[alvo]?.focus()
  })

  /** Remove a ficha `indice`. Com `levarFoco`, o foco segue a regra da
   *  seção (a): seguinte, senão anterior, senão input. Sem ele (Backspace no
   *  input), o foco fica onde está. */
  function remover(indice: number, levarFoco: boolean) {
    const proxima = value.filter((_, i) => i !== indice)
    if (levarFoco) {
      focoPendente.current = proxima.length === 0 ? 'input' : Math.min(indice, proxima.length - 1)
    }
    onChange(proxima)
  }

  /** Enter ou vírgula: UMA palavra. Recusada, o texto fica e a mensagem diz
   *  o motivo. */
  function tentarAdicionar(bruto: string) {
    if (cheio) return
    const resultado = validarPalavra(bruto)
    if (!resultado.ok) {
      setMensagemLocal(resultado.motivo)
      return
    }
    if (indiceDaPalavra(value, resultado.normalizada) >= 0) {
      setMensagemLocal(`«${resultado.palavra}» já está na lista.`)
      return
    }
    onChange([...value, resultado.palavra])
    setTexto('')
    setMensagemLocal(undefined)
  }

  /** Colar: várias de uma vez. As válidas entram (duplicata é pulada em
   *  silêncio — colar a mesma lista duas vezes não é erro); as inválidas e as
   *  que não couberam no limite FICAM no input, para nada se perder, e a
   *  mensagem explica a primeira inválida. */
  function adicionarVarias(partes: readonly string[]) {
    const lista = [...value]
    const restantes: string[] = []
    let primeiroMotivo: string | undefined

    for (const parte of partes) {
      const bruto = parte.trim()
      if (bruto === '') continue
      if (lista.length >= MAX_PALAVRAS_CHAVE) {
        restantes.push(bruto)
        continue
      }
      const resultado = validarPalavra(bruto)
      if (!resultado.ok) {
        primeiroMotivo ??= resultado.motivo
        restantes.push(bruto)
        continue
      }
      if (indiceDaPalavra(lista, resultado.normalizada) >= 0) continue
      lista.push(resultado.palavra)
    }

    if (lista.length !== value.length) onChange(lista)
    setTexto(restantes.join(', '))
    setMensagemLocal(primeiroMotivo)
  }

  function aoDigitar(event: ChangeEvent<HTMLInputElement>) {
    setTexto(event.target.value)
    if (mensagemLocal) setMensagemLocal(undefined)
  }

  function aoTeclarNoInput(event: KeyboardEvent<HTMLInputElement>) {
    if (event.nativeEvent.isComposing) return
    const input = event.currentTarget

    if (event.key === 'Enter' || event.key === ',') {
      // Sempre prevenido: é o que impede a submissão implícita do formulário
      // a partir deste campo — com ou sem texto.
      event.preventDefault()
      if (input.value.trim() !== '') tentarAdicionar(input.value)
      return
    }

    if (event.key === 'Backspace' && input.value === '' && value.length > 0) {
      event.preventDefault()
      remover(value.length - 1, false)
      return
    }

    if (
      event.key === 'ArrowLeft' &&
      value.length > 0 &&
      input.selectionStart === 0 &&
      input.selectionEnd === 0
    ) {
      event.preventDefault()
      botoesRef.current[value.length - 1]?.focus()
      return
    }

    // Qualquer outra tecla apaga a mensagem da tentativa anterior.
    if (mensagemLocal) setMensagemLocal(undefined)
  }

  function aoColar(event: ClipboardEvent<HTMLInputElement>) {
    const colado = event.clipboardData.getData('text')
    if (!/[,\n\r]/.test(colado)) return
    event.preventDefault()
    if (cheio) return
    const input = event.currentTarget
    const inicio = input.selectionStart ?? input.value.length
    const fim = input.selectionEnd ?? input.value.length
    const inteiro = input.value.slice(0, inicio) + colado + input.value.slice(fim)
    adicionarVarias(inteiro.split(/[,\n\r]+/))
  }

  function aoTeclarNaFicha(event: KeyboardEvent<HTMLButtonElement>, indice: number) {
    switch (event.key) {
      case 'ArrowLeft':
        event.preventDefault()
        botoesRef.current[indice - 1]?.focus()
        return
      case 'ArrowRight':
        event.preventDefault()
        if (indice + 1 < value.length) botoesRef.current[indice + 1]?.focus()
        else inputRef.current?.focus()
        return
      case 'Delete':
      case 'Backspace':
        event.preventDefault()
        remover(indice, true)
        return
      // Enter e Space: o clique nativo do <button> cuida, e cai em `remover`
      // pelo onClick — mesma regra de foco.
      default:
        return
    }
  }

  /** Clique no vão da caixa (fora de ficha e de input) leva o foco ao input:
   *  a caixa inteira parece um campo, então a caixa inteira se comporta como
   *  um. Só quando o alvo é a própria caixa — clicar num × continua sendo
   *  clicar no ×. */
  function aoApertarNaCaixa(event: PointerEvent<HTMLDivElement>) {
    if (event.target !== event.currentTarget) return
    event.preventDefault()
    inputRef.current?.focus()
  }

  return (
    <FieldShell
      id={inputId}
      messageId={mensagemId}
      label={label}
      labelAction={
        <output
          id={contadorId}
          aria-live="polite"
          className={styles.contador}
          data-full={cheio || undefined}
        >
          {value.length} de {MAX_PALAVRAS_CHAVE}
        </output>
      }
      hint={cheio ? DICA_NO_LIMITE : hint}
      error={erro}
    >
      <div
        className={styles.caixa}
        data-invalid={erro ? 'true' : undefined}
        onPointerDown={aoApertarNaCaixa}
      >
        <ul className={styles.lista} aria-label="Palavras-chave adicionadas">
          {/* A palavra é a chave: única por construção, já que duplicata (pela
              forma normalizada) não entra. Remover uma ficha mantém as outras
              montadas — e o × que vai receber o foco é o mesmo nó. */}
          {value.map((palavra, indice) => {
            const marcada = invalidIndex === indice && error !== undefined
            return (
              <li
                key={palavra}
                className={styles.ficha}
                data-invalid={marcada ? 'true' : undefined}
              >
                {marcada ? (
                  <span className={styles.alerta}>
                    <AlertIcon size={14} />
                  </span>
                ) : null}
                <span className={styles.texto}>{palavra}</span>
                <button
                  type="button"
                  tabIndex={-1}
                  className={styles.remover}
                  aria-label={`Remover ${palavra}`}
                  ref={(el) => {
                    botoesRef.current[indice] = el
                  }}
                  onClick={() => remover(indice, true)}
                  onKeyDown={(event) => aoTeclarNaFicha(event, indice)}
                >
                  <CloseIcon size={14} />
                </button>
              </li>
            )
          })}
        </ul>
        <input
          ref={inputRef}
          id={inputId}
          type="text"
          className={styles.input}
          value={texto}
          onChange={aoDigitar}
          onKeyDown={aoTeclarNoInput}
          onPaste={aoColar}
          readOnly={cheio}
          autoComplete="off"
          autoCapitalize="off"
          spellCheck={false}
          enterKeyHint="enter"
          aria-describedby={`${contadorId} ${mensagemId} ${instrucaoId}`}
          aria-invalid={erro ? true : undefined}
        />
      </div>
      <span id={instrucaoId} className="sr-only">
        {INSTRUCAO}
      </span>
    </FieldShell>
  )
}
